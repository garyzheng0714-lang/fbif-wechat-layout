package main

// feishu_review_card_icons.go — 审核聚合卡按钮图标（2026-10-09 用户要求按钮带 Lucide 图标）。
//
// 飞书卡片按钮只认 standard_icon（飞书自带图标）或 custom_icon（上传所得 img_key），
// 不能直接用 SVG，也不会给 custom_icon 着色。所以 Lucide 图标按所在按钮的文字颜色
// 预先出 PNG（tools/gen-review-card-icons.sh → cardicons/），运行时由发卡的审核应用
// 自行上传拿 img_key 并按应用缓存（img_key 与上传应用绑定）。
//
// 卡片构建只在按钮上写一个内部标记 reviewCardIconField；buildReviewBatchCardWithIcons
// 收尾时统一换成 custom_icon，拿不到 img_key 就去掉标记、按钮照常无图标——图标永远
// 不阻塞发卡。飞书 2.0 对未知字段报错，标记绝不能留在发出去的卡里。

import (
	"context"
	"embed"
	"log"
	"strings"
	"sync"
	"time"
)

//go:embed cardicons/*.png
var reviewCardIconFS embed.FS

const reviewCardIconField = "_review_icon"

// Lucide 图标名（与 cardicons/<名>-<配色>.png 对应）。
const (
	reviewIconWechat     = "wechat-favicon" // 原文（mp.weixin.qq.com 网站图标）
	reviewIconSource     = "external-link"  // 原网页
	reviewIconResync     = "refresh-cw"     // 重新同步至FoodTalks
	reviewIconUnpublish  = "eye-off"        // 下线
	reviewIconPublish    = "send"           // 发布 / 强制发布 / 重新发布
	reviewIconDistribute = "share-2"        // 同步分发至其他平台
	reviewIconAttention  = "circle-alert"   // 检查未完成
	reviewIconHeader     = "network"        // 卡片头部：一篇文章分发到多个平台（浅蓝，用户 2026-10-10 选定）
)

// reviewCardIcons 「图标名-配色」→ 当前审核应用上传所得 img_key。
type reviewCardIcons map[string]string

func withReviewIcon(button map[string]any, icon string) map[string]any {
	button[reviewCardIconField] = icon
	return button
}

// reviewCardIconVariant 按钮最终形态决定图标配色，与 tools/gen-review-card-icons.sh 一致。
func reviewCardIconVariant(button map[string]any) string {
	if disabled, _ := button["disabled"].(bool); disabled {
		return "disabled"
	}
	switch button["type"] {
	case "primary_filled":
		return "inverse"
	case "danger":
		return "danger"
	default:
		return "default"
	}
}

func applyReviewCardIcons(node any, icons reviewCardIcons) {
	switch value := node.(type) {
	case map[string]any:
		if icon, ok := value[reviewCardIconField].(string); ok {
			delete(value, reviewCardIconField)
			// Lucide 图标按按钮配色各有一张（<名>-<配色>）；网站图标是品牌色，只有一张（<名>）。
			key := icons[icon+"-"+reviewCardIconVariant(value)]
			if key == "" {
				key = icons[icon]
			}
			if key != "" {
				value["icon"] = map[string]any{"tag": "custom_icon", "img_key": key}
			}
		}
		for _, child := range value {
			applyReviewCardIcons(child, icons)
		}
	case []any:
		for _, child := range value {
			applyReviewCardIcons(child, icons)
		}
	case []map[string]any:
		for _, child := range value {
			applyReviewCardIcons(child, icons)
		}
	}
}

// reviewCardIconCache 按应用缓存图标 img_key。上传失败后同一应用 5 分钟内不重试，
// 免得飞书图片接口故障时每张卡都卡在上传上。
type reviewCardIconCache struct {
	mu       sync.Mutex
	keys     map[string]reviewCardIcons
	failedAt map[string]time.Time
}

const reviewCardIconRetryAfter = 5 * time.Minute

func (c *reviewCardIconCache) forNotifier(ctx context.Context, n *feishuNotifier) reviewCardIcons {
	if n == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	app := n.baseURL + "|" + n.appID
	if c.keys == nil {
		c.keys, c.failedAt = map[string]reviewCardIcons{}, map[string]time.Time{}
	}
	icons := c.keys[app]
	if icons == nil {
		icons = reviewCardIcons{}
		c.keys[app] = icons
	}
	entries, err := reviewCardIconFS.ReadDir("cardicons")
	if err != nil {
		return icons.clone()
	}
	if failed, ok := c.failedAt[app]; ok && time.Since(failed) < reviewCardIconRetryAfter {
		return icons.clone()
	}
	uploadCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	delete(c.failedAt, app)
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".png")
		if icons[name] != "" {
			continue
		}
		data, err := reviewCardIconFS.ReadFile("cardicons/" + entry.Name())
		if err != nil {
			continue
		}
		key, err := n.uploadImageBytes(uploadCtx, entry.Name(), data)
		if err != nil {
			log.Printf("publish: WARN 审核卡按钮图标上传失败（本卡缺图标的按钮照常显示）: %v", err)
			c.failedAt[app] = time.Now()
			break
		}
		icons[name] = key
	}
	return icons.clone()
}

func (icons reviewCardIcons) clone() reviewCardIcons {
	out := make(reviewCardIcons, len(icons))
	for name, key := range icons {
		out[name] = key
	}
	return out
}

// reviewCardIconsForJob 用这张卡将要投递的审核应用准备图标；未启用投递配置（旧单群
// notifier）时不带图标。
func (p *publishPipeline) reviewCardIconsForJob(ctx context.Context, job publishJob) reviewCardIcons {
	if p == nil || p.deliverySettings == nil {
		return nil
	}
	settings, apps, err := p.reviewDeliveryForJob(job)
	if err != nil || len(settings.ChatIDs) == 0 {
		return nil
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, settings.ChatIDs[0])
	if err != nil {
		return nil
	}
	return p.reviewCardIconCache.forNotifier(ctx, notifier)
}
