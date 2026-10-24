package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
)

// 2026-10-24 外发账号改用多维表格「平台账号」的 record_id（distribution_accounts.go）。之前的 lane 与改稿记录用云梭
// 账号 UUID 当键；服务启动时、任务恢复和后台 worker 起来之前，把还挂着云梭 UUID 的改挂到这篇能发的号里同平台的
// 那一个：只换键，intent、幂等键、冻结内容、状态、回执原样（分发服务按原来的幂等键照样查得到），云梭的编号留在
// lane 的 YunsuoAccountID 上对云梭回执；提醒签名跟着换算，不会因此再发一轮。只在启动时改，所以不会撞上推到一半、
// 查到一半的 lane。同平台不止一个号、这篇已有那个号的记录、两个旧编号落到同一个号，都不改、记一次日志；没改挂的
// 旧 lane 在，这篇在那个平台上就不再推（externalUnadoptedLaneCovers），免得新编号查不到旧 lane、又给同一个号存一篇。
// 部署候选暂停态不改：回滚回去的旧版本认不得新键。删云梭时（迁移第 3 步）连同这个文件一起删。

var externalAdoptionSkipsLogged sync.Map

func (p *publishPipeline) adoptExternalAccountIDs(accounts []externalAccount) error {
	if publishWorkersPaused() {
		return nil
	}
	jobs, err := p.store.all()
	if err != nil {
		return err
	}
	cfg := externalTargetsFrom(accounts)
	known := map[string]bool{}
	for _, account := range accounts {
		known[account.ID] = true
	}
	platforms := legacyExternalAccountPlatforms(jobs)
	for _, job := range jobs {
		if moved, _ := planExternalAccountAdoption(job, cfg, known, platforms); len(moved) == 0 {
			continue
		}
		var moved map[string]string
		_, swapped, err := p.store.updateIf(job.ID, func(current publishJob) bool {
			var skipped []string
			moved, skipped = planExternalAccountAdoption(current, cfg, known, platforms)
			for _, why := range skipped {
				if _, seen := externalAdoptionSkipsLogged.LoadOrStore(fmt.Sprintf("%d/%s", current.ID, why), true); !seen {
					log.Printf("publish: #%d 外发记录没改挂：%s", current.ID, why)
				}
			}
			return len(moved) > 0
		}, func(current *publishJob) {
			applyExternalAccountAdoption(current, moved)
		})
		if err != nil {
			return fmt.Errorf("#%d 外发记录改挂失败：%w", job.ID, err)
		}
		if swapped {
			log.Printf("publish: #%d 外发记录改挂到「平台账号」：%v", job.ID, moved)
		}
	}
	return nil
}

// legacyYunsuoAccountID 云梭账号编号是 UUID（8-4-4-4-12 位十六进制）；表的 record_id 不会长这样。
func legacyYunsuoAccountID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, r := range id {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
	}
	return true
}

// legacyExternalAccountPlatforms 每个云梭编号是哪个平台：存草稿的 lane 看冻结内容里的平台，经云梭的看 lane 上的叫法；
// 同一个编号在不同稿上认出不同平台的，不认。
func legacyExternalAccountPlatforms(jobs []publishJob) map[string]string {
	out := map[string]string{}
	for _, job := range jobs {
		for _, lane := range job.ExternalDistributionLanes {
			id := strings.TrimSpace(lane.AccountID)
			if !legacyYunsuoAccountID(id) {
				continue
			}
			platform := legacyExternalLanePlatform(lane)
			if previous, seen := out[id]; seen && previous != platform {
				platform = ""
			}
			out[id] = platform
		}
	}
	return out
}

func legacyExternalLanePlatform(lane publishExternalLane) string {
	if lane.Driver == externalDriverRobot {
		var payload robotDraftPayload
		if json.Unmarshal([]byte(lane.PayloadJSON), &payload) == nil && strings.TrimSpace(payload.Platform) != "" {
			return strings.TrimSpace(payload.Platform)
		}
	}
	profile := yunsuoProfileForTarget(externalAccount{Name: lane.Name + " " + lane.PlatformName})
	if profile.Key == yunsuoGenericProfile.Key {
		return ""
	}
	return profile.Key
}

// planExternalAccountAdoption 这篇里哪些云梭编号改挂到哪个号（旧 → 新），以及没改的原因。
func planExternalAccountAdoption(job publishJob, cfg externalTargets, known map[string]bool, platforms map[string]string) (map[string]string, []string) {
	present := map[string]bool{}
	for _, lane := range job.ExternalDistributionLanes {
		present[strings.TrimSpace(lane.AccountID)] = true
	}
	for _, draft := range job.ExternalDistributionDrafts {
		present[strings.TrimSpace(draft.AccountID)] = true
	}
	moved := map[string]string{}
	var skipped []string
	for id := range present {
		if known[id] || !legacyYunsuoAccountID(id) {
			continue
		}
		platform := platforms[id]
		if platform == "" {
			skipped = append(skipped, id+" 认不出是哪个平台")
			continue
		}
		var match []externalAccount
		for _, target := range cfg.forJob(job) {
			if target.Platform == platform {
				match = append(match, target)
			}
		}
		switch {
		case len(match) != 1:
			skipped = append(skipped, fmt.Sprintf("%s 在「平台账号」里有 %d 个这篇能发的%s号", id, len(match), platform))
		case present[match[0].ID]:
			skipped = append(skipped, fmt.Sprintf("%s 与 %s 是同一个号，这篇两个编号下都有记录", id, match[0].ID))
		default:
			moved[id] = match[0].ID
		}
	}
	landed := map[string]int{}
	for _, to := range moved {
		landed[to]++
	}
	for id, to := range moved {
		if landed[to] > 1 {
			delete(moved, id)
			skipped = append(skipped, fmt.Sprintf("%s 与别的云梭编号都落到 %s", id, to))
		}
	}
	return moved, skipped
}

// externalUnadoptedLaneCovers 这篇在 target 的平台上还有没改挂的云梭旧 lane（认不准跳过的、启动时没读到账号的）：
// 算这个号推过了，新建、重试、对账重发都不走，免得给同一个号再存一篇。认不出平台的旧 lane 挡住所有平台。
func externalUnadoptedLaneCovers(job publishJob, target externalAccount, cfg externalTargets) bool {
	for _, lane := range job.ExternalDistributionLanes {
		id := strings.TrimSpace(lane.AccountID)
		if id == target.ID || !legacyYunsuoAccountID(id) {
			continue
		}
		if _, current := cfg.target(id); current {
			continue
		}
		if platform := legacyExternalLanePlatform(lane); platform == "" || platform == target.Platform {
			return true
		}
	}
	return false
}

func applyExternalAccountAdoption(job *publishJob, moved map[string]string) {
	before := externalReminderSignature(*job)
	for i := range job.ExternalDistributionLanes {
		lane := &job.ExternalDistributionLanes[i]
		if to, ok := moved[strings.TrimSpace(lane.AccountID)]; ok {
			if lane.Driver != externalDriverRobot && lane.YunsuoAccountID == "" {
				lane.YunsuoAccountID = lane.AccountID
			}
			lane.AccountID = to
		}
	}
	for i := range job.ExternalDistributionDrafts {
		if to, ok := moved[strings.TrimSpace(job.ExternalDistributionDrafts[i].AccountID)]; ok {
			job.ExternalDistributionDrafts[i].AccountID = to
		}
	}
	// 签名里带账号键：只是换了键，提醒内容没变，跟着换算，免得被当成新的一轮。
	if r := &job.ExternalDistributionReminder; r.Signature == before {
		r.Signature = externalReminderSignature(*job)
	}
}
