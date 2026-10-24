package main

// publish_external_fidelity.go — 外发保真核对：分发服务存完草稿当场重新打开读回，与送去的正文逐项比
// （标题、小标题、加粗、图片、逐条图注、封面），结果随回答带回来，记在 lane 的 Fidelity
// （publish_external_robot.go robotDraftFidelity）。对不上就在审核卡这个平台的行里写一句灰字，
// GET /api/publish/monitoring 给 warning（不是 P0）。
//
// 云梭（2026-10-24 下线）推出去的旧 lane 是事后取线上正文比的，结果照旧显示，不再取。

import "time"

const (
	// 读回失败的按取满次数记，monitoring 报「取不到」。
	externalFidelityMaxAttempts = 3
	// 对不上或一直取不到的，monitoring 报 7 天。
	externalFidelityWarnFor = 7 * 24 * time.Hour
)

// externalFidelityShape 一篇正文看上去的结构。
type externalFidelityShape struct {
	// Headings 小标题与整段加粗的段（平台上两者都是加粗段）。
	Headings        int `json:"headings"`
	Images          int `json:"images"`
	CaptionedImages int `json:"captioned_images"`
}

// publishExternalFidelity 一条 lane 的核对结果。CheckedAt 有值就是读回比过了。
type publishExternalFidelity struct {
	CheckedAt   string                `json:"checked_at,omitempty"`
	Attempts    int                   `json:"attempts,omitempty"`
	LastTriedAt string                `json:"last_tried_at,omitempty"`
	Error       string                `json:"error,omitempty"`
	Expected    externalFidelityShape `json:"expected,omitzero"`
	Online      externalFidelityShape `json:"online,omitzero"`
	// Note 对不上的地方（「草稿读回对不上：…」，云梭旧 lane 是「平台上小标题少了 1 处」）；空表示一致。
	Note string `json:"note,omitempty"`
}

// externalLaneFidelityNote 卡片与侧边栏的一句：平台上对不上的地方（经分发服务的草稿见 robotPlatformState）。
func externalLaneFidelityNote(lane publishExternalLane) string {
	if lane.State != publishExternalDistributionSucceeded || lane.Fidelity.CheckedAt == "" {
		return ""
	}
	return lane.Fidelity.Note
}

// externalFidelityMonitoring 7 天内核对出对不上的，与取满次数仍取不到线上正文的 lane 数。
func externalFidelityMonitoring(job publishJob, now time.Time) (mismatched, unavailable int) {
	for _, lane := range job.ExternalDistributionLanes {
		check := lane.Fidelity
		switch {
		case check.CheckedAt != "" && check.Note != "" && externalFidelityRecent(now, check.CheckedAt):
			mismatched++
		case check.CheckedAt == "" && check.Attempts >= externalFidelityMaxAttempts && externalFidelityRecent(now, check.LastTriedAt):
			unavailable++
		}
	}
	return mismatched, unavailable
}

func externalFidelityRecent(now time.Time, value string) bool {
	at, err := time.Parse(time.RFC3339, value)
	return err == nil && now.Sub(at) <= externalFidelityWarnFor
}
