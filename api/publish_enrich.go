package main

// publish_enrich.go — M5「AI 预填发布参数」（施工图 WORKLOG-publish-pipeline-in-layout-
// 2026-07-10.md M5 节）。enriching 阶段（mirroring 后 staging 前，此时正文图已 OSS 化，
// 模型看到的封面候选就是最终 URL）调 MiMo 多模态（ai_models.go），产出：
//
//	cover_url（从候选图里选一张）/ summary（10-40 字一句话钩子）/ tags（1-3 个标签）
//	/ author（从正文识别的作者名）/ notes（2-4 句人话优化说明）
//
// 🚨 边界（施工图钉死）：本文件只产**元数据**，绝不碰 job.ContentHTML——AI 没有正文
// 篡改面；排版修补是另一条线（届时上三道铁闸）。
//
// 封面要看图，模型必须真的收到图片（2026-10-07 用户裁定：看图用 Flash）。此前
// 「豆包 A + 纯文本 DeepSeek B + 纯文本判决」已删除：看不见图的 B 与判决凭网址顺序
// 编造了「歪马送酒实物照」，把页脚商务名片选成了 #263 的封面。候选图也只认原文自带
// 的图（enrichCandidateImages），网站页脚的名片/二维码/logo 永远不进候选。
//
// 降级哲学：AI 是加速器不是闸门。MIMO_API_KEY 未配置 / 调用失败 / 解析失败 →
// fallbackEnrichment 规则兜底（封面维持 mirroring 已定值、摘要=正文纯文本前 40 字、
// author/source_name 留空、tags 空、notes 写明未执行原因），WARN 不 fail job，
// 阶段照常推进。

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
)

// 输入裁剪（控 token 成本，够模型判断即可）：正文纯文本最多 6000 字、候选图最多前 8 张。
const (
	aiEnrichMaxBodyRunes = 6000
	aiEnrichMaxImages    = 8
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

// aiEnrichArticle 生产默认实现：多模态调 MiMo 看图档（aiTierVision，标题 + 纯文本
// 正文 + 候选图 input_image），要求严格 JSON 输出并容错解析。失败返回 error，由
// stageEnrich 统一走规则兜底——本函数自身不兜底，职责单一。
func aiEnrichArticle(ctx context.Context, job publishJob) (publishEnrichment, error) {
	if _, _, err := aiCredential(ctx); err != nil {
		return publishEnrichment{}, err
	}
	var images []string
	if strings.TrimSpace(job.SourceCoverURL) == "" {
		images = enrichCandidateImages(job) // 有公众号原封面时封面已定，不再花多模态 token 选图
	}
	bodyText := truncateRunes(htmlToPlainText(job.ContentHTML), aiEnrichMaxBodyRunes)
	evidence := job.Title + "\n" + bodyText

	userText := "文章标题：" + job.Title + "\n\n正文（纯文本）：\n" + bodyText
	if len(images) > 0 {
		userText += fmt.Sprintf("\n\n下面附 %d 张候选封面图，每张图紧挨在它自己的「候选图 N：URL」标注之后。"+
			"cover_url 必须原样抄写你所选那张图正上方标注里的 URL，不要按序号推算。", len(images))
	} else if strings.TrimSpace(job.SourceCoverURL) != "" {
		userText += "\n\n封面已沿用公众号原封面，cover_url 请给空字符串。"
	} else {
		userText += "\n\n本文没有可用的候选封面图，cover_url 请给空字符串。"
	}
	// 每张图前紧贴自己的 URL 标注：2026-10-07 实测只在开头列 URL 清单时，模型看对了图
	// 却抄错相邻一张的 URL（#263 想选歪马海报、返回了时间线图表）。
	userContent := []map[string]any{{"type": "input_text", "text": userText}}
	for i, u := range images {
		userContent = append(userContent,
			map[string]any{"type": "input_text", "text": fmt.Sprintf("候选图 %d：%s", i+1, u)},
			map[string]any{"type": "input_image", "image_url": u})
	}
	text, err := aiResponsesCall(ctx, aiModel(aiTierVision), editorialEnrichSystemPrompt(), userContent)
	if err != nil {
		return publishEnrichment{}, err
	}
	result, err := parseEnrichmentJSON(text, images)
	if err != nil {
		return publishEnrichment{}, err
	}
	return enforceEnrichmentEvidence(result, evidence), nil
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

// enforceEnrichmentEvidence 把「作者/出处绝不编造」从 prompt 与离线评测提升为
// 运行时硬约束。模型只能抽取本次标题/正文里实际出现的值；找不到证据就留空，
// 同时在 notes 留痕让审核人知道发生过自动清理。空格差异（Mote 莫特/Mote莫特）
// 不应造成误删，故比较前只折叠大小写并移除 Unicode 空白。
func enforceEnrichmentEvidence(result publishEnrichment, sourceText string) publishEnrichment {
	haystack := normalizeEnrichmentEvidence(sourceText)
	dropped := make([]string, 0, 2)
	if result.Author != "" && !strings.Contains(haystack, normalizeEnrichmentEvidence(result.Author)) {
		result.Author = ""
		dropped = append(dropped, "作者")
	}
	if result.SourceName != "" && !strings.Contains(haystack, normalizeEnrichmentEvidence(result.SourceName)) {
		result.SourceName = ""
		dropped = append(dropped, "出处")
	}
	if len(dropped) > 0 {
		note := "未在标题或正文找到" + strings.Join(dropped, "、") + "证据，已自动留空。"
		if strings.TrimSpace(result.Notes) == "" {
			result.Notes = note
		} else {
			result.Notes = strings.TrimSpace(result.Notes) + " " + note
		}
	}
	result.Notes = truncateRunes(strings.TrimSpace(result.Notes), 600)
	return result
}

func normalizeEnrichmentEvidence(value string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value)))
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

// enrichCandidateImages 封面候选 = 当前封面（公众号原封面或正文首图，已 OSS 化）+
// 原文自带的正文图（mirroring 后已是 OSS URL），跳过 data: 内联图，最多 8 张（多模态
// 每张图都是真金白银的 token）。网站页脚的名片/二维码/logo 来自规则素材而非原文，
// 一律不进候选（#263 页脚名片被选成封面的根因之一）。
func enrichCandidateImages(job publishJob) []string {
	article := publishArticleImageSet(job)
	srcs := extractImgSrcs(job.ContentHTML)
	var out []string
	seen := map[string]bool{}
	add := func(src string) {
		src = strings.TrimSpace(src)
		if src == "" || strings.HasPrefix(src, "data:") || seen[src] || len(out) >= aiEnrichMaxImages {
			return
		}
		seen[src] = true
		out = append(out, src)
	}
	if !publishCoverIsTemplateImage(job.CoverURL, srcs, article) {
		add(job.CoverURL)
	}
	for _, src := range srcs {
		if article.has(src) {
			add(src)
		}
	}
	return out
}

// publishCoverIsTemplateImage 封面是网站页脚/模板图（出现在成品里却不来自原文）。
// 公众号原封面通常不出现在正文里，原文配图在 article 里，两者都不算模板图；旧任务
// 遗留的页脚名片封面（#263、#5）命中这里，必须作废重选。
func publishCoverIsTemplateImage(cover string, srcs []string, article publishArticleImages) bool {
	id := publishImageIdentity(cover)
	if id == "" || article.has(cover) {
		return false
	}
	for _, src := range srcs {
		if publishImageIdentity(src) == id {
			return true
		}
	}
	return false
}

// publishArticleImages 原文自带图片的集合：BlocksJSON 里的 img src，以及它们经
// ImageMap 转存后的 OSS URL。比较忽略 query/fragment（mmbiz 同图常带不同 wx_fmt 参数）。
type publishArticleImages map[string]bool

func publishArticleImageSet(job publishJob) publishArticleImages {
	set := publishArticleImages{}
	var blocks []Block
	if err := json.Unmarshal([]byte(job.BlocksJSON), &blocks); err != nil {
		return set
	}
	imageMap := map[string]string{}
	if strings.TrimSpace(job.ImageMap) != "" {
		_ = json.Unmarshal([]byte(job.ImageMap), &imageMap)
	}
	mirrored := map[string]string{}
	for src, oss := range imageMap {
		mirrored[publishImageIdentity(src)] = oss
	}
	for _, b := range blocks {
		src := b.Src
		if b.K == "video" {
			src = b.Poster
		} else if b.K != "img" {
			continue
		}
		if strings.TrimSpace(src) == "" {
			continue
		}
		id := publishImageIdentity(src)
		set[id] = true
		if oss := mirrored[id]; oss != "" {
			set[publishImageIdentity(oss)] = true
		}
	}
	return set
}

func (s publishArticleImages) has(src string) bool { return s[publishImageIdentity(src)] }

func publishImageIdentity(src string) string {
	src = strings.TrimSpace(html.UnescapeString(src))
	if i := strings.IndexAny(src, "?#"); i >= 0 {
		src = src[:i]
	}
	src = strings.TrimPrefix(strings.TrimPrefix(src, "https:"), "http:")
	return strings.ToLower(strings.TrimPrefix(src, "//"))
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
