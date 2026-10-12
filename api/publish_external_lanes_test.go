package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLane(accountID, name, state string) publishExternalLane {
	return publishExternalLane{
		AccountID: accountID, Name: name, State: state,
		RequestedBy: publishExternalAutoActor, RequestedAt: "2026-10-10T01:00:00Z",
	}
}

func setTestLanes(t *testing.T, env *publishTestEnv, jobID int64, lanes ...publishExternalLane) publishJob {
	t.Helper()
	job, err := env.store.update(jobID, func(current *publishJob) {
		current.ExternalDistributionLanes = append([]publishExternalLane(nil), lanes...)
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func mustLane(t *testing.T, job publishJob, accountID string) publishExternalLane {
	t.Helper()
	lane, ok := externalLaneFor(job, accountID)
	if !ok {
		t.Fatalf("任务 #%d 缺少账号 %s 的 lane：%+v", job.ID, accountID, job.ExternalDistributionLanes)
	}
	return lane
}

// TestLegacyExternalDistributionMigratesToLanes 按平台拆开前的单轮数据读盘即迁成 lanes：
// 共用单号的拆成各查各的；还没推的旧 payload 拆成单账号，绝不带着别的账号再推；
// 当天的补发存档也迁过来；迁完旧字段不再写回。
func TestLegacyExternalDistributionMigratesToLanes(t *testing.T) {
	store := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	create := func(key string, mutate func(*publishJob)) int64 {
		job, _, err := store.create(key, "https://mp.weixin.qq.com/s/"+key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.update(job.ID, mutate); err != nil {
			t.Fatal(err)
		}
		return job.ID
	}
	both := []publishExternalDistributionTarget{
		{AccountID: testYunsuoToutiaoID, Name: "头条号", State: publishExternalTargetSucceeded},
		{AccountID: testYunsuoBaijiaID, Name: "百家号", State: publishExternalTargetFailed, Remark: "登录失效"},
	}
	partial := create("partial", func(j *publishJob) {
		j.LegacyExternalState = publishExternalDistributionPartialFailed
		j.LegacyExternalContentPK = "pk-shared"
		j.LegacyExternalRequestedAt = "2026-10-06T01:00:00Z"
		j.LegacyExternalTargets = both
	})
	queuedPayload, _ := json.Marshal(yunsuoPushPayload{Title: "旧稿", AccountIDs: []string{testYunsuoToutiaoID, testYunsuoBaijiaID}, HTML: "<p>正文</p>"})
	queued := create("queued", func(j *publishJob) {
		j.LegacyExternalState = publishExternalDistributionQueued
		j.LegacyExternalPayloadJSON = string(queuedPayload)
		j.LegacyExternalPayloadHash = "sha256:old"
		j.LegacyExternalTargets = []publishExternalDistributionTarget{
			{AccountID: testYunsuoToutiaoID, Name: "头条号", State: publishExternalTargetQueued},
			{AccountID: testYunsuoBaijiaID, Name: "百家号", State: publishExternalTargetQueued},
		}
	})
	timedOut := create("timeout", func(j *publishJob) {
		j.LegacyExternalState = publishExternalDistributionReconciliationRequired
		j.LegacyExternalContentPK = "pk-timeout"
		j.LegacyExternalTargets = []publishExternalDistributionTarget{
			{AccountID: testYunsuoToutiaoID, Name: "头条号", State: publishExternalTargetSucceeded},
			{AccountID: testYunsuoBaijiaID, Name: "百家号", State: publishExternalTargetPending},
		}
	})
	preFail := create("prefail", func(j *publishJob) {
		j.LegacyExternalState = publishExternalDistributionFailed
		j.LegacyExternalTargets = []publishExternalDistributionTarget{{AccountID: testYunsuoToutiaoID, Name: "头条号", State: publishExternalTargetFailed}}
	})
	supplement := create("supplement", func(j *publishJob) {
		j.LegacyExternalHistory = []publishExternalDistributionRound{{
			State: publishExternalDistributionSucceeded, ContentPK: "pk-toutiao", RequestedBy: publishExternalAutoActor,
			RequestedAt: "2026-10-10T01:00:00Z",
			Targets:     []publishExternalDistributionTarget{{AccountID: testYunsuoToutiaoID, Name: "头条号", State: publishExternalTargetSucceeded}},
		}}
		j.LegacyExternalState = publishExternalDistributionPolling
		j.LegacyExternalContentPK = "pk-baijia"
		j.LegacyExternalRequestedBy = publishExternalAutoBackfillActor
		j.LegacyExternalTargets = []publishExternalDistributionTarget{{AccountID: testYunsuoBaijiaID, Name: "百家号", State: publishExternalTargetPending}}
	})
	noTargets := create("notargets", func(j *publishJob) {
		j.LegacyExternalState = publishExternalDistributionSucceeded
	})

	read := func(id int64) publishJob {
		job, err := store.byID(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.LegacyExternalState != "" || len(job.LegacyExternalTargets) != 0 || len(job.LegacyExternalHistory) != 0 {
			t.Fatalf("迁完旧字段应清空：%+v", job)
		}
		return job
	}
	job := read(partial)
	if l := mustLane(t, job, testYunsuoToutiaoID); l.State != publishExternalDistributionSucceeded || l.ContentPK != "pk-shared" {
		t.Fatalf("头条号应迁为成功并保留单号：%+v", l)
	}
	if l := mustLane(t, job, testYunsuoBaijiaID); l.State != publishExternalDistributionFailed || l.ContentPK != "pk-shared" ||
		l.Remark != "登录失效" || !publishExternalLaneNeedsLateRefresh(l, mustParseRFC3339(t, "2026-10-12T00:00:00Z")) {
		t.Fatalf("百家号应迁为有单号的失败、继续补查：%+v", l)
	}

	job = read(queued)
	for _, id := range []string{testYunsuoToutiaoID, testYunsuoBaijiaID} {
		l := mustLane(t, job, id)
		var payload yunsuoPushPayload
		if err := json.Unmarshal([]byte(l.PayloadJSON), &payload); err != nil || l.State != publishExternalDistributionQueued ||
			len(payload.AccountIDs) != 1 || payload.AccountIDs[0] != id || !strings.HasPrefix(l.PayloadHash, "sha256:") || l.PayloadHash == "sha256:old" {
			t.Fatalf("未推的旧 payload 应拆成单账号：%+v payload=%+v", l, payload)
		}
	}

	job = read(timedOut)
	if mustLane(t, job, testYunsuoToutiaoID).State != publishExternalDistributionSucceeded ||
		mustLane(t, job, testYunsuoBaijiaID).State != publishExternalDistributionReconciliationRequired {
		t.Fatalf("对账中的旧轮次：已成功的账号按成功，其余仍待核对：%+v", job.ExternalDistributionLanes)
	}
	if l := mustLane(t, read(preFail), testYunsuoToutiaoID); !externalLaneCanSafeRetry(l) {
		t.Fatalf("受理前失败应迁为可安全重推：%+v", l)
	}
	job = read(supplement)
	if l := mustLane(t, job, testYunsuoToutiaoID); l.State != publishExternalDistributionSucceeded || l.ContentPK != "pk-toutiao" {
		t.Fatalf("补发存档应迁为头条号成功：%+v", l)
	}
	if l := mustLane(t, job, testYunsuoBaijiaID); l.State != publishExternalDistributionPolling || l.ContentPK != "pk-baijia" ||
		l.RequestedBy != publishExternalAutoBackfillActor {
		t.Fatalf("补发轮应迁为百家号轮询中：%+v", l)
	}
	job = read(noTargets)
	if _, all := externalDistributionCoveredAccounts(job); !all {
		t.Fatalf("缺账号的旧轮次应视为所有账号都推过，绝不自动再推：%+v", job.ExternalDistributionLanes)
	}
}

func mustParseRFC3339(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
