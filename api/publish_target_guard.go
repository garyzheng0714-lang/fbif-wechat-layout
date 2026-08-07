package main

// publish_target_guard.go — FoodTalks 环境守卫（2026-08-06 切正式端前置）。
//
// 背景：publish job 的 FoodTalksNewsID 只是一个数字，测试站与正式站的资讯 id
// 各自独立编号。切换 FOODTALKS_BASE_URL 后，拿着测试站草稿 id 去正式站做
// UpdateNews / PreviewToken，会命中正式站上一篇完全无关的真实文章——PUT 是全量
// 覆盖语义，等于把别人的文章整篇改掉。因此每个持有锚点的任务都记录草稿建在
// 哪个环境（staging_target），跨环境的读写一律拒绝。
//
// 兼容口径：staging_target 为空的存量任务（字段上线前创建）在测试/未配置环境
// 保持原行为；当前环境是 production 时对空值 fail closed——正式站上的每一次
// UpdateNews 都必须能证明锚点属于正式站。

import (
	"fmt"
	"os"
	"strings"
)

// currentFoodTalksTarget 当前服务指向的 FoodTalks 环境：test / production /
// custom / unconfigured（口径与 readiness 的 foodTalksURLTarget 完全一致）。
func currentFoodTalksTarget() string {
	return foodTalksURLTarget(strings.TrimSpace(os.Getenv(foodTalksBaseURLEnv)))
}

// publishStagingTargetMismatch 校验任务锚点与当前环境是否一致。返回 nil 表示
// 允许操作；返回错误时调用方必须拒绝外调，不得降级继续。
func publishStagingTargetMismatch(job publishJob) error {
	if job.FoodTalksNewsID <= 0 {
		return nil
	}
	current := currentFoodTalksTarget()
	staged := strings.TrimSpace(job.StagingTarget)
	if staged == current {
		return nil
	}
	if staged == "" && current != "production" {
		return nil
	}
	stagedLabel := staged
	if stagedLabel == "" {
		stagedLabel = "未知（早期任务，无环境记录）"
	}
	return fmt.Errorf(
		"环境守卫：任务 #%d 的 FoodTalks 草稿（资讯 id=%d）建在 %s 环境，当前服务指向 %s 环境，禁止跨环境操作该资讯 id；如需在当前环境发布，请先重新暂存出本环境草稿",
		job.ID, job.FoodTalksNewsID, stagedLabel, current)
}
