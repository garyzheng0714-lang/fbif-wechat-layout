package main

// publish_enrich.go — M5「AI 预填发布参数」（施工图 WORKLOG-publish-pipeline-in-layout-
// 2026-07-10.md M5 节）。enriching 阶段（mirroring 后 staging 前，此时正文图已 OSS 化，
// 模型看到的封面候选就是最终 URL）调豆包 ARK responses API，产出：
//
//	cover_url（从正文图候选里选一张）/ summary（80-120 字摘要）/ tags（3-5 个标签）
//	/ author（从正文识别的作者名）/ notes（2-4 句人话优化说明）
//
// 🚨 边界（施工图钉死）：本文件只产**元数据**，绝不碰 job.ContentHTML——AI 没有正文
// 篡改面；排版修补是另一条线（届时上三道铁闸）。
//
// 降级哲学：AI 是加速器不是闸门。ARK_API_KEY 未配置 / 调用失败 / 解析失败 →
// fallbackEnrichment 规则兜底（封面维持 mirroring 已定的正文第一图、摘要=正文纯文本
// 前 100 字、author=source_name、tags 空、notes 写明未执行原因），WARN 不 fail job，
// 阶段照常推进。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// ARK 可配项：API Key 必配（缺失走兜底）；模型与 Base URL 有默认值，env 可覆盖
// （Base URL 覆盖同时服务测试 httptest 桩，与 FOODTALKS_BASE_URL 同模式）。
const (
	arkAPIKeyEnv      = "ARK_API_KEY"
	arkModelIDEnv     = "ARK_MODEL_ID"
	arkBaseURLEnv     = "ARK_BASE_URL"
	defaultARKModelID = "ep-20260623134011-qljpn" // Doubao-Seed-2.1-pro endpoint
	defaultARKBaseURL = "https://ark.cn-beijing.volces.com"
	arkResponsesPath  = "/api/v3/responses"
)

// 输入裁剪（控 token 成本，够模型判断即可）：正文纯文本最多 6000 字、候选图最多前 8 张。
const (
	arkEnrichMaxBodyRunes = 6000
	arkEnrichMaxImages    = 8
)

// system prompt 见 editorial_norms.go（M7：从编辑部历史文章倒推的规范 + 词表组装，
// 语言强制约束「请用简体中文回复」已写在 norms_prompt.md 里）。

// publishEnrichment enriching 阶段的产出（AI 或规则兜底）。
type publishEnrichment struct {
	CoverURL   string
	Summary    string
	Tags       []string
	Author     string
	SourceName string // 正文声明的原始出处（「本文来自微信公众号：XX」），没有则空
	Notes      string
}

// enrichMaxTags 编辑部口径每篇 1~3 个标签（规范文档 §4），模型超发时硬截断。
const enrichMaxTags = 3

// arkEnrichArticle 生产默认实现：多模态调 ARK responses API（标题 + 纯文本正文 +
// 正文图 input_image），要求严格 JSON 输出并容错解析。任何失败都返回 error，
// 由 stageEnrich 统一降级兜底——本函数自身不兜底，职责单一。
func arkEnrichArticle(ctx context.Context, job publishJob) (publishEnrichment, error) {
	apiKey := strings.TrimSpace(os.Getenv(arkAPIKeyEnv))
	if apiKey == "" {
		return publishEnrichment{}, errors.New("ARK_API_KEY 未配置")
	}
	images := enrichCandidateImages(job.ContentHTML)
	bodyText := truncateRunes(htmlToPlainText(job.ContentHTML), arkEnrichMaxBodyRunes)

	userText := "文章标题：" + job.Title + "\n\n正文（纯文本）：\n" + bodyText
	if len(images) > 0 {
		userText += "\n\n候选封面图 URL（按正文出现顺序，cover_url 必须从中原样选一个）：\n" + strings.Join(images, "\n")
	} else {
		userText += "\n\n本文正文没有可用图片，cover_url 请给空字符串。"
	}
	userContent := []map[string]any{{"type": "input_text", "text": userText}}
	for _, u := range images {
		userContent = append(userContent, map[string]any{"type": "input_image", "image_url": u})
	}
	payload, err := json.Marshal(map[string]any{
		"model": arkModelID(),
		"input": []any{
			map[string]any{"role": "system", "content": []map[string]any{{"type": "input_text", "text": editorialEnrichSystemPrompt()}}},
			map[string]any{"role": "user", "content": userContent},
		},
	})
	if err != nil {
		return publishEnrichment{}, fmt.Errorf("序列化 ARK 请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, arkBaseURL()+arkResponsesPath, bytes.NewReader(payload))
	if err != nil {
		return publishEnrichment{}, fmt.Errorf("构造 ARK 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := arkHTTPClient.Do(req)
	if err != nil {
		return publishEnrichment{}, fmt.Errorf("ARK 接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return publishEnrichment{}, fmt.Errorf("读 ARK 响应失败: %w", err)
	}
	text, err := arkExtractOutputText(respBody, resp.StatusCode)
	if err != nil {
		return publishEnrichment{}, err
	}
	return parseEnrichmentJSON(text, images)
}

// arkHTTPClient 多模态推理慢（要看图、要思考长文）：2026-07-10 评测实测 120s 会让
// 40% 的长文超时（走兜底浪费 AI），放宽到 300s——enriching 是异步阶段，慢无妨；
// 测试桩即回不受影响。
var arkHTTPClient = &http.Client{Timeout: 300 * time.Second}

// arkExtractOutputText responses API 响应 → 模型输出文本：遍历 output[].content[] 拼接
// output_text（跳过 reasoning 等非 message 项），HTTP/业务错误显式报错。
func arkExtractOutputText(respBody []byte, statusCode int) (string, error) {
	var out struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("ARK 响应不是预期 JSON（HTTP %d）: %w", statusCode, err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return "", fmt.Errorf("ARK 调用失败（HTTP %d）: %s", statusCode, out.Error.Message)
	}
	if statusCode != http.StatusOK {
		return "", fmt.Errorf("ARK 调用失败（HTTP %d）", statusCode)
	}
	var sb strings.Builder
	for _, item := range out.Output {
		if item.Type != "" && item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Type == "output_text" {
				sb.WriteString(c.Text)
			}
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", errors.New("ARK 响应没有模型输出文本")
	}
	return text, nil
}

// parseEnrichmentJSON 模型输出文本 → publishEnrichment。容错：剥 markdown 代码围栏、
// 截取首尾大括号之间的 JSON。cover_url 必须原样命中候选列表，否则弃用（防模型幻觉
// URL 混进发布参数——封面维持 mirroring 已定值）。
func parseEnrichmentJSON(text string, candidates []string) (publishEnrichment, error) {
	cleaned := stripMarkdownFence(text)
	if start, end := strings.Index(cleaned, "{"), strings.LastIndex(cleaned, "}"); start >= 0 && end > start {
		cleaned = cleaned[start : end+1]
	}
	var parsed struct {
		CoverURL   string   `json:"cover_url"`
		Summary    string   `json:"summary"`
		Tags       []string `json:"tags"`
		Author     string   `json:"author"`
		SourceName string   `json:"source_name"`
		Notes      string   `json:"notes"`
	}
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return publishEnrichment{}, fmt.Errorf("模型输出不是合法 JSON: %w", err)
	}
	cover := strings.TrimSpace(parsed.CoverURL)
	if cover != "" {
		valid := false
		for _, c := range candidates {
			if c == cover {
				valid = true
				break
			}
		}
		if !valid {
			cover = "" // 幻觉/改写的 URL 一律弃用
		}
	}
	// 标签：去空白、去重、按编辑部口径硬截断到 1~3 个（规范文档 §4）。
	tags := make([]string, 0, enrichMaxTags)
	seen := map[string]bool{}
	for _, tag := range parsed.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
		if len(tags) >= enrichMaxTags {
			break
		}
	}
	return publishEnrichment{
		CoverURL:   cover,
		Summary:    strings.TrimSpace(parsed.Summary),
		Tags:       tags,
		Author:     strings.TrimSpace(parsed.Author),
		SourceName: strings.TrimSpace(parsed.SourceName),
		Notes:      strings.TrimSpace(parsed.Notes),
	}, nil
}

// fallbackEnrichment 规则兜底（施工图钉死）：封面不动（mirroring 已用正文第一图兜底
// 且 OSS 化）、摘要=正文纯文本前 40 字（编辑部摘要 84% ≤40 字，规范文档 §3——规则
// 兜底写不出钩子，至少长度不越线）、author 空（编辑部 132/163 篇作者为空且宁空勿造，
// 规范 §5；来源信息由 source_name 承载不塞 author）、tags 空、notes 写明未执行原因
// （一句话，截断防错误串刷屏卡片）。
func fallbackEnrichment(job publishJob, cause error) publishEnrichment {
	reason := "未知原因"
	if cause != nil {
		reason = truncateRunes(strings.Join(strings.Fields(cause.Error()), " "), 120)
	}
	return publishEnrichment{
		CoverURL: "", // 空 = 维持 job.CoverURL 现值
		Summary:  truncateRunes(htmlToPlainText(job.ContentHTML), 40),
		Notes:    "AI 预填未执行（" + reason + "）",
	}
}

// enrichCandidateImages 封面候选 = 正文 img src（mirroring 后已是 OSS URL），
// 跳过 data: 内联图，最多前 8 张（多模态每张图都是真金白银的 token）。
func enrichCandidateImages(contentHTML string) []string {
	var out []string
	for _, src := range extractImgSrcs(contentHTML) {
		if strings.HasPrefix(src, "data:") {
			continue
		}
		out = append(out, src)
		if len(out) >= arkEnrichMaxImages {
			break
		}
	}
	return out
}

var (
	// htmlBlockBreakRe 块级收尾/换行标签 → 换行，防剥标签后词句粘连。
	htmlBlockBreakRe = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|section|h[1-6]|li|tr|blockquote|figcaption)>`)
	htmlAnyTagRe     = regexp.MustCompile(`(?s)<[^>]*>`)
)

// htmlToPlainText 正文 HTML → 纯文本（喂模型 + 摘要兜底用，不追求排版完美）：
// 块级标签转换行 → 剥所有标签 → 实体解码 → 逐行去空白。
func htmlToPlainText(rawHTML string) string {
	s := htmlBlockBreakRe.ReplaceAllString(rawHTML, "\n")
	s = htmlAnyTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// stripMarkdownFence 剥 ```json ... ``` / ``` ... ``` 围栏（模型即使被叮嘱也可能加）。
func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:] // 丢掉 ```json 这一行的语言标注
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

// truncateRunes 按字符数截断（中文一个字算一个，不能按字节截出半个汉字）。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// arkModelID / arkBaseURL env 优先，默认常量兜底（同 siteFrontURL 模式）。
func arkModelID() string {
	if v := strings.TrimSpace(os.Getenv(arkModelIDEnv)); v != "" {
		return v
	}
	return defaultARKModelID
}

func arkBaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv(arkBaseURLEnv)), "/"); v != "" {
		return v
	}
	return defaultARKBaseURL
}
