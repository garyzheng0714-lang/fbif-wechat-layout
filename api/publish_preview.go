package main

// publish_preview.go — 未上架预览页（施工图「预览链接」节，2026-07-10 用户裁定：
// 审核必须给未上架的预览链接，不能盲审）。GET /preview/publish/{id}?t=<sig> 渲染
// job 的 content_html（就是将要发布的字节）+ 标题 + AI 预填参数页尾。
//
// 安全模型：
//   - 签名 t = hex(HMAC-SHA256(key=由 AUTH_SESSION_SECRET 派生的子密钥, msg="publish-preview:"+id))——
//     免登录可分享给审核人，但不可遍历、不可伪造；未发布内容不裸奔。
//   - 签名错 / 任务不存在 / 阶段太早，一律同一个 404（不区分成因，防探测遍历）。
//   - AUTH_SESSION_SECRET 未配置（签名无密钥可算）→ 503。
//   - 仅 awaiting_review 及之后阶段（awaiting_review/publicizing/published/rejected）
//     可预览——此时 content_html 已定型；更早阶段（含 failed 半成品）404。
//
// 页面：白底、内容区 max-width 680px 居中（近似 FoodTalks 前台阅读版式）、顶部通栏
// 警示黄横幅「预览 · 未发布」。content_html 是本流水线自产可信内容，原样嵌入；
// 标题与 AI 参数等文本字段一律 HTML 转义。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// defaultPublishPreviewBaseURL 生产站点域名。取值顺序：WECHAT_UPLOAD_ORIGIN（deploy.yml
// 已写死为本站生产域名）→ 本常量兜底——不新增平行 env（最少新概念）。
const defaultPublishPreviewBaseURL = "https://fbifmp-layout.foodtalks.cn"

// publishPreviewBaseURL 预览链接的站点前缀（飞书卡片按钮等外发场景用）。
func publishPreviewBaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("WECHAT_UPLOAD_ORIGIN")), "/"); v != "" {
		return v
	}
	return defaultPublishPreviewBaseURL
}

// publishPreviewSig 预览签名：HMAC-SHA256(key=由 AUTH_SESSION_SECRET 派生的独立子密钥,
// "publish-preview:"+id)。规则后台不再有共享密码；未配置时返回空串，端点侧 503。
func publishPreviewSig(jobID int64) string {
	secret := strings.TrimSpace(os.Getenv("AUTH_SESSION_SECRET"))
	if secret == "" {
		return ""
	}
	derived := sha256.Sum256(append([]byte("fbif-publish-preview\x00"), []byte(secret)...))
	mac := hmac.New(sha256.New, derived[:])
	mac.Write([]byte("publish-preview:" + strconv.FormatInt(jobID, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// publishPreviewURL 完整预览链接（卡片「预览效果」按钮 open_url 用）。
func publishPreviewURL(jobID int64) string {
	return publishPreviewBaseURL() + "/preview/publish/" + strconv.FormatInt(jobID, 10) + "?t=" + publishPreviewSig(jobID)
}

// publishPreviewAllowed 仅 awaiting_review 及之后阶段可预览（content_html 已定型）。
// failed 不在序列里：failed_stage 可能停在 fetching/rendering 半成品，不给看。
func publishPreviewAllowed(stage string) bool {
	switch stage {
	case publishStageAwaitingReview, publishStagePublicizing, publishStagePublished, publishStageRejected:
		return true
	}
	return false
}

func registerPublishPreviewRoute(mux *http.ServeMux, store *publishJobStore) {
	mux.HandleFunc("GET /preview/publish/{id}", func(w http.ResponseWriter, r *http.Request) {
		if publishPreviewSig(0) == "" {
			http.Error(w, "预览服务未配置", http.StatusServiceUnavailable)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		want := publishPreviewSig(id)
		got := r.URL.Query().Get("t")
		// 常量时间比对；签名不匹配与任务不存在同响应，不给探测面。
		if got == "" || !hmac.Equal([]byte(got), []byte(want)) {
			http.NotFound(w, r)
			return
		}
		job, err := store.byID(id)
		if err != nil || !publishPreviewAllowed(job.Stage) {
			http.NotFound(w, r)
			return
		}
		// 官方预览优先（2026-07-10 用户裁定「发布预览会有专门的预览链接」）：现铸
		// 短期 token（每次点击都新铸，避免复用已过期 token）302 到 FoodTalks 官方预览页——网站
		// 真实渲染，视觉保真度最高。铸造失败 / 草稿未进站 / ?local=1 → 本地预览页兜底
		//（本地页仍展示 AI 参数全量，且不依赖 FoodTalks 可用性）。
		if job.FoodTalksNewsID > 0 && r.URL.Query().Get("local") != "1" {
			if ft, ferr := newFoodTalksClientFromEnv(); ferr == nil {
				if tok, terr := ft.PreviewToken(r.Context(), job.FoodTalksNewsID); terr == nil {
					if target, targetErr := officialPublishPreviewURL(siteFrontURL(), job.FoodTalksNewsID, tok); targetErr == nil {
						// 302 也必须禁 Referer：否则浏览器会把当前带 HMAC 签名的本地预览 URL
						// 泄漏到 FoodTalks 访问日志。重定向本身也禁止缓存/索引。
						w.Header().Set("Referrer-Policy", "no-referrer")
						w.Header().Set("Cache-Control", "no-store")
						w.Header().Set("X-Robots-Tag", "noindex, nofollow")
						http.Redirect(w, r, target, http.StatusFound)
						return
					} else {
						log.Printf("publish: WARN 官方预览目标 URL 无效，回落本地预览页: %v", targetErr)
					}
				} else {
					log.Printf("publish: WARN 官方预览 token 铸造失败，回落本地预览页: %v", terr)
				}
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src https: data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Write([]byte(renderPublishPreviewHTML(job)))
	})
}

func officialPublishPreviewURL(front string, newsID int64, token string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(front))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		return "", fmt.Errorf("FoodTalks 前台 URL 无效")
	}
	if newsID <= 0 || strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("FoodTalks 官方预览缺 news id 或 token")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/preview/news/" + strconv.FormatInt(newsID, 10)
	base.RawPath = ""
	query := base.Query()
	query.Set("token", token)
	base.RawQuery = query.Encode()
	base.Fragment = ""
	return base.String(), nil
}

// renderPublishPreviewHTML 纯服务端拼页面。content_html 是流水线自产可信字节原样嵌入；
// 其余文本字段全部转义。
func renderPublishPreviewHTML(job publishJob) string {
	title := html.EscapeString(job.Title)
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>预览 · `)
	b.WriteString(title)
	b.WriteString(`</title>
<style>
body { margin: 0; background: #ffffff; color: #333; font-family: -apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", sans-serif; }
.preview-banner { background: #fff4ce; color: #664d03; border-bottom: 1px solid #f0dc9a; padding: 12px 16px; text-align: center; font-size: 14px; font-weight: 600; position: sticky; top: 0; }
.preview-main { max-width: 680px; margin: 0 auto; padding: 24px 16px 48px; }
.preview-title { font-size: 27px; font-weight: 700; line-height: 1.4; margin: 0 0 24px; }
.preview-content img { max-width: 100%; height: auto; }
.preview-meta { margin-top: 48px; border-top: 1px solid #e5e5e5; padding-top: 16px; font-size: 12px; color: #888; line-height: 1.8; }
.preview-meta h2 { font-size: 13px; color: #666; margin: 0 0 8px; }
.preview-meta .cover-thumb { max-width: 160px; height: auto; display: block; border: 1px solid #e5e5e5; border-radius: 4px; margin: 4px 0 8px; }
</style>
</head>
<body>
<div class="preview-banner">预览 · 未发布——本页仅供审核，实际上线以 FoodTalks 为准</div>
<main class="preview-main">
<h1 class="preview-title">`)
	b.WriteString(title)
	b.WriteString(`</h1>
<div class="preview-content">`)
	b.WriteString(job.ContentHTML) // 自产可信字节，原样 = 将要发布的正文
	b.WriteString(`</div>
<footer class="preview-meta">
<h2>AI 预填参数（仅供审核参考）</h2>
`)
	if job.CoverURL != "" {
		b.WriteString(`<div>封面：</div><img class="cover-thumb" src="`)
		b.WriteString(html.EscapeString(job.CoverURL))
		b.WriteString(`" alt="封面缩略图">`)
		b.WriteString("\n")
	}
	writePreviewMetaLine(&b, "摘要", job.Summary)
	writePreviewMetaLine(&b, "标签", strings.Join(job.Tags, "、"))
	writePreviewMetaLine(&b, "作者", job.Author)
	// 来源判定（M7 确定性规则）：审核人要能看到将写进网站的转载/原创口径与来源名。
	sourceType, sourceName := siteSourceFor(job)
	sourceLabel := "原创"
	if sourceType == 1 {
		sourceLabel = "转载"
	}
	writePreviewMetaLine(&b, "来源", sourceLabel+" · "+sourceName)
	writePreviewMetaLine(&b, "优化说明", job.AINotes)
	writePreviewMetaLine(&b, "风险审核", publishRiskLevelLabel(job.RiskLevel))
	for i, item := range job.RiskItems {
		writePreviewMetaLine(&b, "风险 "+strconv.Itoa(i+1)+" · "+publishRiskCategoryLabel(item.Category), item.Evidence+"；原因："+item.Reason)
	}
	writePreviewMetaLine(&b, "风险说明", job.RiskNotes)
	b.WriteString(`</footer>
</main>
</body>
</html>`)
	return b.String()
}

func writePreviewMetaLine(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		value = "-"
	}
	b.WriteString(`<div>`)
	b.WriteString(label)
	b.WriteString(`：`)
	b.WriteString(html.EscapeString(value))
	b.WriteString("</div>\n")
}
