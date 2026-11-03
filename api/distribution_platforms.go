package main

// distribution_platforms.go — 各第三方平台的限制，只在分发服务的平台文件里（分发框架边界 2，
// docs/adr/2026-10-24-distribution-framework.md）：排版工具经 GET /v1/platforms 取，最后一次成功的存数据目录
// distribution-platforms.json（distribution_accounts.go 的 distributionCache）。平台文件只在分发服务发版时变，
// 所以只在启动时、中台配置包换了版本时（加号、换号）、用到时一份都没有时取，兜底每 PLATFORMS_FALLBACK_SECONDS（默认 6 小时）一次。
// 这里只按取到的数推送前检查、在卡片和改稿页提示、给自动外发分名额、核对文章链接域名；正文写成什么标签由分发服务定。
// 分发服务连不上就接着用上一份；从没取到过某个平台时卡片照常显示，推送在提交前失败（没检查过的不算通过）。

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// externalDriver lane 上记的送稿方式：robot 是自己的分发服务；空是 2026-10-24 下线的云梭留下的旧 lane，只显示。
type externalDriver string

const (
	externalDriverRobot externalDriver = "robot"

	platformsFile = "distribution-platforms.json"

	// 数法（分发服务 service/limits.mjs 的 COUNTS，distributionTextLength 照同一张表数）：chars 每个字符算一个；
	// ascii_half 英文字母、数字、半角符号和空格两个算一个，其余一个；bytes_half 空白不算，编号 0–255 的字符算半个，
	// @、# 和其余字符算一个，剩半个算一个（头条编辑器数图注的法子）。
	titleCountChars     = "chars"
	titleCountASCIIHalf = "ascii_half"
	countBytesHalf      = "bytes_half"

	// distributionPlatformsRetry 一份都没有时，用到就取，但两次之间至少隔这么久（分发服务连不上时不追着问）。
	distributionPlatformsRetry = 5 * time.Minute
)

// distributionPlatformInfo 分发服务 GET /v1/platforms 给的一个平台（原样存进本地文件）。
type distributionPlatformInfo struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Carrier    string                `json:"carrier"`
	Title      distributionTitleRule `json:"title"`
	Cover      distributionCoverRule `json:"cover"`
	Caption    distributionTextRule  `json:"caption"`
	DailyLimit int                   `json:"daily_limit"`
	Hosts      []string              `json:"hosts"`
	ManageURL  string                `json:"manage_url"`
}

type distributionTitleRule struct {
	Min   int    `json:"min"`
	Max   int    `json:"max"`
	Count string `json:"count"`
}

// distributionTextRule 图注每条最多几个字、怎么数；Max 是 0 表示平台不限。
type distributionTextRule struct {
	Max   int    `json:"max"`
	Count string `json:"count"`
}

type distributionCoverRule struct {
	MinWidth  int   `json:"min_width"`
	MinHeight int   `json:"min_height"`
	MaxBytes  int64 `json:"max_bytes"`
}

var distributionPlatformIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// validateDistributionPlatforms 网络上取到的、本地文件里读出的都过一遍；有一项不对就整份不要，接着用上一份。
func validateDistributionPlatforms(items []distributionPlatformInfo) error {
	seen := map[string]bool{}
	for i, p := range items {
		where := fmt.Sprintf("分发服务给的第 %d 个平台", i+1)
		if !distributionPlatformIDPattern.MatchString(p.ID) || strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("%s缺编号或名字", where)
		}
		if seen[p.ID] {
			return fmt.Errorf("分发服务给了重复的平台 %s", p.ID)
		}
		seen[p.ID] = true
		if p.Carrier != externalCarrierArticle {
			continue // 贴图平台不归排版工具：贴图线在贴图发布项目（2026-10-27）
		}
		if p.Title.Min < 1 || p.Title.Max < p.Title.Min {
			return fmt.Errorf("%s（%s）标题字数范围不对：%d–%d", where, p.ID, p.Title.Min, p.Title.Max)
		}
		if p.Title.Count != titleCountChars && p.Title.Count != titleCountASCIIHalf {
			return fmt.Errorf("%s（%s）标题数法「%s」排版工具不认得", where, p.ID, p.Title.Count)
		}
		if p.Caption.Max < 0 || p.Caption.Max > 0 && !distributionCountKnown(p.Caption.Count) {
			return fmt.Errorf("%s（%s）图注上限 %d、数法「%s」排版工具不认得", where, p.ID, p.Caption.Max, p.Caption.Count)
		}
		if p.Cover.MinWidth < 0 || p.Cover.MinHeight < 0 || p.DailyLimit < 0 {
			return fmt.Errorf("%s（%s）封面下限或每天上限是负数", where, p.ID)
		}
		for _, host := range p.Hosts {
			if strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/: ") {
				return fmt.Errorf("%s（%s）文章链接域名「%s」不对", where, p.ID, host)
			}
		}
		if p.ManageURL != "" && !strings.HasPrefix(p.ManageURL, "https://") {
			return fmt.Errorf("%s（%s）后台地址要是 https", where, p.ID)
		}
	}
	return nil
}

var (
	distributionPlatforms = &distributionCache[distributionPlatformInfo]{
		what: "平台限制", key: "platforms", validate: validateDistributionPlatforms,
	}
	distributionPlatformsKick = make(chan struct{}, 1)
)

// kickDistributionPlatforms 请取平台限制的循环取一次；已经在排队就不重复排。
func kickDistributionPlatforms() {
	select {
	case distributionPlatformsKick <- struct{}{}:
	default:
	}
}

// watchDistributionPlatforms 取平台限制的循环：有人请（kickDistributionPlatforms）就取，兜底每 PLATFORMS_FALLBACK_SECONDS 一次。
func watchDistributionPlatforms(now bool) {
	for {
		if !now {
			select {
			case <-distributionPlatformsKick:
			case <-time.After(intervals.platformsFallback):
			}
		}
		now = false
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := distributionPlatforms.refresh(ctx); err != nil {
			log.Printf("publish: 取%s失败，接着用上次的：%v", distributionPlatforms.what, err)
		}
		cancel()
	}
}

// distributionPlatform 一个平台的限制，推送前检查、卡片、改稿页、提醒、配额、链接登记都用它。
// Known=false 是还没从分发服务拿到这个平台：名字退回账号名，限制都是零值，不能拿来判断通过（见 buildRobotDraftPayload）。
type distributionPlatform struct {
	Key   string
	Name  string
	Known bool
	// 封面下限，读得到图片尺寸时才拦；0 表示平台没有下限。
	CoverMinWidth, CoverMinHeight int
	// 标题原样送，TitleMin–TitleMax 是字数范围（按 titleLength 的数法）。
	TitleMin, TitleMax int
	// 标题里的英文字母、数字、半角符号两个算一个字（title.count 是 ascii_half）。
	TitleHalfWidthASCII bool
	// 图注每条最多几个字（按 CaptionCount 的数法）；0 表示平台不限。超了推之前缩写（publish_external_captions.go）。
	CaptionMax   int
	CaptionCount string
	// 摘要字数上限：分发服务现在不收摘要、也不给上限，所以是 0（不查）；哪天平台要摘要，接口加上再填。
	SummaryMax int
	// 平台每天的文章上限，自动外发按账号各自计数（人工按钮发的也计入）；0 表示不自动发。
	DailyLimit int
	// 文章链接所在域名：人工登记已发链接、分发服务给的草稿地址都只认这些域名（子域名也算）。
	URLHosts []string
	// 平台后台的内容管理页：没拿到文章或草稿链接时，卡片与侧边栏给这个链接看情况。
	ManageURL string
}

func (i distributionPlatformInfo) platform() distributionPlatform {
	return distributionPlatform{
		Key: i.ID, Name: strings.TrimSpace(i.Name), Known: true,
		CoverMinWidth: i.Cover.MinWidth, CoverMinHeight: i.Cover.MinHeight,
		TitleMin: i.Title.Min, TitleMax: i.Title.Max, TitleHalfWidthASCII: i.Title.Count == titleCountASCIIHalf,
		CaptionMax: i.Caption.Max, CaptionCount: i.Caption.Count,
		DailyLimit: i.DailyLimit, URLHosts: append([]string(nil), i.Hosts...), ManageURL: i.ManageURL,
	}
}

// distributionPlatformsByKey 一份平台限制快照，按平台编号查。读不到时是空的（都算没拿到），并请循环去取。
func distributionPlatformsByKey() map[string]distributionPlatform {
	items, err := distributionPlatforms.snapshot()
	if err != nil && distributionPlatforms.idleFor(distributionPlatformsRetry) {
		kickDistributionPlatforms()
	}
	out := make(map[string]distributionPlatform, len(items))
	for _, item := range items {
		if item.Carrier == externalCarrierArticle {
			out[item.ID] = item.platform()
		}
	}
	return out
}

// platformByKey 按平台编号取（冻结内容、旧 lane 上记的平台）；没拿到过就是 Known=false，名字退回编号。
func platformByKey(key string) distributionPlatform {
	if p, ok := distributionPlatformsByKey()[key]; ok {
		return p
	}
	return distributionPlatform{Key: key, Name: firstNonEmpty(strings.TrimSpace(key), "第三方平台")}
}

// platformForAccount 这个号所在平台的限制：取账号目录时一并记下的那份（一次操作用同一份快照），没有再现查。
func platformForAccount(account externalAccount) distributionPlatform {
	if account.platform.Known {
		return account.platform
	}
	p := platformByKey(strings.TrimSpace(account.Platform))
	if !p.Known {
		p.Name = firstNonEmpty(strings.TrimSpace(account.Name), p.Name)
	}
	return p
}

// Icon 审核卡分发区平台块里的平台图标（api/cardicons/platform-<平台编号>.png，平台自己的网站图标）。
func (p distributionPlatform) Icon() string { return "platform-" + p.Key }

// unknownNote 还没拿到这个平台的限制时，推送前给人看的话。
func (p distributionPlatform) unknownNote() string {
	return fmt.Sprintf("还没从分发服务拿到%s的平台限制（标题字数、封面要求），没检查过的稿不推；分发服务恢复后再存一次", p.Name)
}

// titleLimits 这个平台标题的字数范围（按 titleLength 的数法）。
func (p distributionPlatform) titleLimits() (int, int) {
	return p.TitleMin, p.TitleMax
}

// titleInputMax 标题输入框按字符数最多能填几个：英文字母、数字两个算一个字的平台，
// 全是半角时可到上限的两倍；超没超由服务端按平台数法判。
func (p distributionPlatform) titleInputMax() int {
	if p.TitleHalfWidthASCII {
		return 2 * p.TitleMax
	}
	return p.TitleMax
}

// titleLength 按这个平台的数法数标题字数（与分发服务 titleLength 同一口径）。
func (p distributionPlatform) titleLength(title string) int {
	if p.TitleHalfWidthASCII {
		return distributionTextLength(titleCountASCIIHalf, title)
	}
	return runeLen(title)
}

// captionLength 按这个平台数图注的法子数（与分发服务 captionsOver 同一口径）。
func (p distributionPlatform) captionLength(caption string) int {
	return distributionTextLength(p.CaptionCount, caption)
}

// captionFits 这条图注没超这个平台的上限（平台不限就都算没超）。
func (p distributionPlatform) captionFits(caption string) bool {
	return p.CaptionMax <= 0 || p.captionLength(caption) <= p.CaptionMax
}

// captionCountNote 跟 AI 说这个平台的图注怎么数字。
func (p distributionPlatform) captionCountNote() string {
	switch p.CaptionCount {
	case countBytesHalf:
		return "汉字和中文标点一个算一个字，英文字母和数字两个算一个字，空格不算"
	case titleCountASCIIHalf:
		return "汉字和中文标点一个算一个字，英文字母、数字和空格两个算一个字"
	}
	return "汉字、字母、标点都算一个字"
}

func distributionCountKnown(count string) bool {
	return count == titleCountChars || count == titleCountASCIIHalf || count == countBytesHalf
}

// distributionTextLength 按分发服务给的数法数字（service/limits.mjs 的 textLength，两边同一口径）。
// bytes_half 照头条编辑器的脚本：去掉空白、&nbsp;、\u0002、\u200b，@ # 和编号 255 以上的字符各两份，
// 其余一份，按 UTF-16 数（表情两个码元算四份），最后除以二往上取整。
func distributionTextLength(count, text string) int {
	switch count {
	case titleCountASCIIHalf:
		wide, ascii := 0, 0
		for _, r := range text {
			if r < 0x80 {
				ascii++
			} else {
				wide++
			}
		}
		return wide + ascii/2
	case countBytesHalf:
		text = strings.Map(func(r rune) rune {
			// JavaScript 的 \s：Go 的 unicode.IsSpace 多认了 U+0085、少认了 U+FEFF。
			if (unicode.IsSpace(r) && r != 0x85) || r == 0xfeff || r == 0x2 || r == 0x200b {
				return -1
			}
			return r
		}, text)
		text = nbspPattern.ReplaceAllString(text, "")
		units := 0
		for _, r := range text {
			switch {
			case r == '@' || r == '#':
				units += 2
			case r <= 0xff:
				units++
			case r > 0xffff:
				units += 4
			default:
				units += 2
			}
		}
		return (units + 1) / 2
	default:
		return runeLen(text)
	}
}

var nbspPattern = regexp.MustCompile(`(?i)(&nbsp;)+`)

// titleCountNote 平台数法与按字符数不同时，提示里补一句怎么数；一样就是空串。
func (p distributionPlatform) titleCountNote() string {
	if p.TitleHalfWidthASCII {
		return "英文字母、数字两个算一个字"
	}
	return ""
}

// titleOver 标题超了平台上限吗（按平台的数法）：返回上限与数出的长度。
func (p distributionPlatform) titleOver(title string) (length, max int, over bool) {
	_, max = p.titleLimits()
	length = p.titleLength(title)
	return length, max, p.Known && length > max
}

// syncAction 卡片、侧边栏与提醒里「送去这个平台」怎么说：存草稿再公开发布，统称同步（2026-10-30 起）。
func (p distributionPlatform) syncAction(name string) string { return "同步至" + name }

func (p distributionPlatform) dailyLimit() int { return p.DailyLimit }

// coverTooSmall 读得到尺寸且小于本平台封面下限；读不到不拦，交给平台判断。
func (p distributionPlatform) coverTooSmall(src string) (int, int, bool) {
	if p.CoverMinWidth <= 0 && p.CoverMinHeight <= 0 {
		return 0, 0, false
	}
	width, height, ok := distributionImageSize(src)
	return width, height, ok && (width < p.CoverMinWidth || height < p.CoverMinHeight)
}
