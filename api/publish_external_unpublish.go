package main

// publish_external_unpublish.go — 把已经发到第三方平台的一篇下线（2026-11-03 #340 读者投稿被误判原创、自动发到
// FBIF 的头条号和百家号，用户：「下，然后卡片也要改」）。
//
// 经分发服务按这条 lane 的幂等键下线（头条号设成仅我可见、百家号设为私密，各平台怎么下见分发服务的平台说明书），
// 分发服务说做成了、平台上查到已不公开，才把 lane 记成「已下线」并刷审核卡；已下线的号不再自动发，卡片上不给
// 重新同步。分发服务还在做（202）就原样说，过一会儿再调同一个接口：它同一篇同一时间只做一件事、已下线的不再点。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	robotActionUnpublish = "unpublish"

	publishExternalAdminUnpublishActor = "system:admin_unpublish"
)

// 分发服务查到的、算下线了的平台现状：仅我可见（百家号私密也算）、已撤回、已删除。
var robotStatesOffline = map[string]bool{"hidden": true, "withdrawn": true, "deleted": true}

var errExternalUnpublishPending = errors.New("分发工具还在下线，过一会儿再调同一个接口")

// unpublishExternalAccount 把这篇在 accountID 那个号上下线；reason 写进卡片那一行。dryRun 只核对能不能下、不碰分发服务。
func (p *publishPipeline) unpublishExternalAccount(ctx context.Context, jobID int64, accountID, reason, actor string, dryRun bool) (publishExternalLane, error) {
	job, err := p.store.byID(jobID)
	if err != nil {
		return publishExternalLane{}, err
	}
	lane, ok := externalLaneFor(job, strings.TrimSpace(accountID))
	if !ok {
		return lane, fmt.Errorf("这篇没有发到 %s", strings.TrimSpace(accountID))
	}
	if lane.State == publishExternalDistributionUnpublished {
		return lane, nil
	}
	if lane.Driver != externalDriverRobot || lane.State != publishExternalDistributionSucceeded || strings.TrimSpace(lane.IntentKey) == "" {
		return lane, fmt.Errorf("%s现在是「%s」：只有分发工具发出去、已发布的能从这里下线", lane.Name, externalPlatformStatus(job, externalAccount{ID: lane.AccountID, Name: lane.Name}).Status)
	}
	payload, profile, ok := robotLanePayload(lane)
	if !ok {
		return lane, errors.New("这条 lane 没有冻结内容，认不出是哪个平台")
	}
	if dryRun {
		return lane, nil
	}
	client, err := p.robotClient()
	if err != nil {
		return lane, err
	}
	record, err := client.Unpublish(ctx, payload.Platform, lane.IntentKey, actor)
	if err != nil {
		return lane, fmt.Errorf("分发工具没下线成：%w", err)
	}
	if action := record.Action; action == nil || action.Kind != robotActionUnpublish || action.Status != robotActionDone {
		return lane, errExternalUnpublishPending
	}
	if record.State == nil || !robotStatesOffline[record.State.Value] {
		now := "没查到"
		if record.State != nil {
			now = "「" + firstNonEmpty(safeActionText(record.State.Raw), record.State.Value) + "」"
		}
		return lane, fmt.Errorf("分发工具说下线做成了，但%s上这篇现在是%s；请到%s后台核对", profile.Name, now, profile.Name)
	}
	stamp := pipelineNow(p).Format(time.RFC3339)
	updated, swapped, err := p.updateExternalLane(jobID, lane.AccountID, func(current publishExternalLane) bool {
		return current.State == publishExternalDistributionSucceeded && current.IntentKey == lane.IntentKey
	}, func(current *publishJob, l *publishExternalLane) {
		l.State = publishExternalDistributionUnpublished
		l.Error, l.Remark = robotLaneReason(reason), robotLaneReason(reason)
		l.CompletedAt = stamp
		enqueueExternalDistributionCardUpdate(current)
	})
	if err != nil {
		return lane, err
	}
	if swapped {
		p.scheduleExternalDistributionCardUpdate(jobID)
	}
	lane, _ = externalLaneFor(updated, lane.AccountID)
	return lane, nil
}
