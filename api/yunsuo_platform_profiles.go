package main

// yunsuo_platform_profiles.go — 多平台分发的平台层。
//
// 2026-10-12 用户裁定：分发规则分两层，底层是各平台通用的（署名、去名片与二维码、
// 去链接、图注合并、文末声明、封面取图、标题截断，见 yunsuo_distribution_rules.go），
// 往下每个平台再有自己的一套。这里只写各平台与通用不同的参数；封面、配额、链接核对都按
// 账号所属平台取。加平台只加一份 profile，不改通用层。
//
// 每个平台还定了稿子经谁送进平台（Driver）：百家号、头条号都经自己的分发服务（FBIF-多平台分发工具，
// 百家号 2026-10-22、头条号 2026-10-24 切过来，只存草稿、不发布），云梭不再接新稿（2026-10-23 用户：
// 「我做这个的目的是要替换掉云梭啊」）；云梭的写法只留给切换前建的旧 lane 续查。正文标签写法跟着送稿的
// 方式走（Markup）：自己的分发服务用编辑器自己的写入通道，平台原生写法留得住（yunsuo_render.go）。

import "strings"

// externalDriver 稿子经谁送进平台。空是云梭：改这个字段之前建的 lane 都没有 driver，一律按云梭续查。
type externalDriver string

const (
	externalDriverYunsuo externalDriver = ""
	externalDriverRobot  externalDriver = "robot"
)

type yunsuoPlatformProfile struct {
	Key  string
	Name string
	// Driver 稿子经谁送进这个平台；建 lane 时抄到 lane 上，之后推送、查询、展示都看 lane 的。
	Driver externalDriver
	// 云梭会把 cover 插到这个平台正文的最前面（百家号经云梭时：#95 8 月稿、#282 都是）。这样的平台，
	// 文章以图开头就用这张图当封面并从正文拿掉，插回来正好在原位。头条号用公众号封面时不插
	// （10-10～10-12 #165 #175 #180 #193 #196 #260），正文不动。
	CoverPrepended bool
	// 封面下限，读得到图片尺寸时才拦；0 表示平台没有已知下限。
	CoverMinWidth, CoverMinHeight int
	// 经云梭的平台：云梭在三档标题里挑一档（yunsuoTitleShort/Medium/Long），人改过的平台标题原样
	// 写进这一档；TitleMax 是推送前检查用的字数上限，0 表示按这一档的上限。
	// 不经云梭的平台没有档位（TitleField 为空），标题原样送，TitleMin–TitleMax 就是字数范围。
	TitleField string
	TitleMin   int
	TitleMax   int
	// 标题里的英文字母、数字、半角符号两个算一个字（头条号）。
	TitleHalfWidthASCII bool
	// 摘要字数上限，推送前检查用；0 表示平台没有已知上限，不查。
	SummaryMax int
	// 平台每天的文章上限，自动外发按账号各自计数（人工按钮发的也计入）。
	DailyLimit int
	// 文章链接所在域名：人工登记已发链接时只认这些域名。
	URLHosts []string
	// 推送成功后怎么找文章链接（publish_external_links.go）；空表示不自动找。
	LinkLookup string
	// 拿到链接后怎么取平台上的正文做保真核对（publish_external_fidelity.go）；空表示不取。
	ContentFetch string
	// 平台后台的内容管理页：已发布但还没拿到文章链接时，卡片与侧边栏给这个链接看发布情况
	// （2026-10-13 用户：「百家号的已发布，我没看到任何的链接。我也查看不了发布情况」）。
	ManageURL string
	// 审核卡分发区平台块里的平台图标（api/cardicons/<Icon>.png，平台自己的网站图标）。
	Icon string
	// 正文标签写法；空表示云梭的写法（yunsuoMarkup）。
	Markup *distributionMarkup
}

func (p yunsuoPlatformProfile) markup() distributionMarkup {
	if p.Markup != nil {
		return *p.Markup
	}
	return yunsuoMarkup
}

const yunsuoDefaultDailyLimit = 5

var yunsuoPlatformProfiles = map[string]yunsuoPlatformProfile{
	// 头条号：经自己的分发服务存草稿（不发布，2026-10-24 从云梭切过来），封面由分发服务在封面弹窗里
	// 上传、不进正文。封面下限来自 2026-10-11 #183 平台回执；每天约 5 篇上限。
	// 文章链接与线上正文核对只对切换前经云梭发出去的旧 lane（存草稿的 lane 没有线上文章）。
	"toutiao": {
		Key: "toutiao", Name: "头条号",
		Driver:        externalDriverRobot,
		Markup:        &toutiaoEditorMarkup,
		CoverMinWidth: 452, CoverMinHeight: 352,
		// 标题 2–30 字，标点与空格都算（2026-10-13 用户裁定），英文字母与数字两个算一个字：
		// 编辑手发的 #287（32 个字符）、#292（35）、#289（42，按此数法正好 30）头条都收了。
		TitleMin: 2, TitleMax: 30, TitleHalfWidthASCII: true,
		DailyLimit:   5,
		URLHosts:     []string{"toutiao.com", "www.toutiao.com", "m.toutiao.com"},
		LinkLookup:   externalLinkLookupToutiao,
		ContentFetch: externalFidelityFetchToutiao,
		ManageURL:    "https://mp.toutiao.com/profile_v4/manage/content/all",
		Icon:         "platform-toutiao",
	},
	// 百家号：经自己的分发服务存草稿（不发布，2026-10-22 用户裁定），封面由分发服务在编辑器的封面
	// 弹窗里上传、不进正文，标题原样送（百家号 2–64 字）。不设封面下限。百度搜索有验证码，文章链接
	// 不自动找、线上正文不取：分发服务存完草稿当场读回核对。
	"baijiahao": {
		Key: "baijiahao", Name: "百家号",
		Driver:     externalDriverRobot,
		Markup:     &baijiahaoEditorMarkup,
		TitleMin:   2,
		TitleMax:   64,
		DailyLimit: yunsuoDefaultDailyLimit,
		URLHosts:   []string{"baijiahao.baidu.com", "mbd.baidu.com"},
		ManageURL:  "https://baijiahao.baidu.com/builder/rc/content",
		Icon:       "platform-baijiahao",
	},
}

// baijiahaoEditorMarkup 自己的分发服务经百家号编辑器的 setContent 写入（2026-10-20/22 实测）：
// h1–h3 都存成 h3（百家号只有一级小标题），strong 留得住，图注写进 div.pgc-img 里的
// p.pgc-img-caption，由分发服务挪进编辑器自己的图注格。
var baijiahaoEditorMarkup = distributionMarkup{
	Heading:  func(text string) string { return "<h3>" + text + "</h3>" },
	BoldLine: pgcBoldLine,
	Image:    pgcImage,
}

// toutiaoEditorMarkup 自己的分发服务经头条编辑器的粘贴通道整篇写入：照编辑手发的 #286–#299，
// 大标题 h1、整段加粗 strong、图注写进 div.pgc-img 里的 p.pgc-img-caption（头条自己的写法，
// 2026-10-23 个人测试号上小标题、加粗、图、图注都读回对上）。
var toutiaoEditorMarkup = distributionMarkup{
	Heading:  func(text string) string { return "<h1>" + text + "</h1>" },
	BoldLine: pgcBoldLine,
	Image:    pgcImage,
}

// 百家号、头条号编辑器共用的写法（两家都是字节系 pgc 编辑器的图注结构）。
func pgcBoldLine(text string) string { return "<p><strong>" + text + "</strong></p>" }

func pgcImage(src, caption string) string {
	if caption == "" {
		return `<p><img src="` + src + `" alt=""></p>`
	}
	return `<div class="pgc-img"><img src="` + src + `" alt=""><p class="pgc-img-caption">` + caption + `</p></div>`
}

// yunsuoGenericProfile 认不出平台的账号：通用版式，不设封面下限。
var yunsuoGenericProfile = yunsuoPlatformProfile{
	Key: "generic", Name: "外部平台",
	TitleField: yunsuoTitleMedium,
	DailyLimit: yunsuoDefaultDailyLimit,
}

// 云梭的三档标题与各自的字数范围（云梭 API 文档：short_title 8–20、title 15–40、
// long_title 20–60；短标题只给「标题最大长度不足 20 字」的平台用）。
const (
	yunsuoTitleShort  = "short"
	yunsuoTitleMedium = "title"
	yunsuoTitleLong   = "long"
)

func yunsuoTitleFieldRange(field string) (int, int) {
	switch field {
	case yunsuoTitleShort:
		return 8, 20
	case yunsuoTitleLong:
		return 20, 60
	default:
		return 15, 40
	}
}

// titleCharRange 标题按字符数的硬范围：经云梭的是云梭这一档的范围，否则是平台自己的范围；
// 英文字母、数字两个算一个字的平台（头条号），全是半角时字符数可到上限的两倍。
func (p yunsuoPlatformProfile) titleCharRange() (int, int) {
	if p.TitleField == "" {
		if p.TitleHalfWidthASCII {
			return p.TitleMin, 2 * p.TitleMax
		}
		return p.TitleMin, p.TitleMax
	}
	return yunsuoTitleFieldRange(p.TitleField)
}

// titleLimits 这个平台标题的字数范围：下限是 titleCharRange 的下限，上限取平台规则与它的上限中较小的。
func (p yunsuoPlatformProfile) titleLimits() (int, int) {
	min, max := p.titleCharRange()
	if p.TitleMax > 0 && p.TitleMax < max {
		max = p.TitleMax
	}
	return min, max
}

// titleLength 按这个平台的数法数标题字数。
func (p yunsuoPlatformProfile) titleLength(title string) int {
	if !p.TitleHalfWidthASCII {
		return runeLen(title)
	}
	wide, ascii := 0, 0
	for _, r := range title {
		if r < 0x80 {
			ascii++
		} else {
			wide++
		}
	}
	return wide + ascii/2
}

// titleCountNote 平台数法与按字符数不同时，提示里补一句怎么数；一样就是空串。
func (p yunsuoPlatformProfile) titleCountNote() string {
	if p.TitleHalfWidthASCII {
		return "英文字母、数字两个算一个字"
	}
	return ""
}

// titleOver 标题推得出去吗：先看按字符算的硬上限，再看平台自己的上限（按平台的数法）。
// 超了返回超的那条上限与按它数出的长度。
func (p yunsuoPlatformProfile) titleOver(title string) (length, max int, over bool) {
	_, fieldMax := p.titleCharRange()
	if n := runeLen(title); n > fieldMax {
		return n, fieldMax, true
	}
	_, max = p.titleLimits()
	length = p.titleLength(title)
	return length, max, length > max
}

func yunsuoProfileForTarget(target yunsuoAccountTarget) yunsuoPlatformProfile {
	key := strings.ToLower(strings.TrimSpace(target.Platform))
	if key == "" {
		switch name := target.Name; {
		case strings.Contains(name, "头条"):
			key = "toutiao"
		case strings.Contains(name, "百家"):
			key = "baijiahao"
		}
	}
	if profile, ok := yunsuoPlatformProfiles[key]; ok {
		return profile
	}
	return yunsuoGenericProfile
}

// syncVerb 卡片、侧边栏与提醒里「送去这个平台」怎么说：云梭直接发布；自己的分发服务目前只存草稿。
func (p yunsuoPlatformProfile) syncVerb() string {
	if p.Driver == externalDriverRobot {
		return "存草稿"
	}
	return "同步"
}

func (p yunsuoPlatformProfile) syncAction(name string) string { return p.syncVerb() + "至" + name }

func (p yunsuoPlatformProfile) dailyLimit() int {
	if p.DailyLimit > 0 {
		return p.DailyLimit
	}
	return yunsuoDefaultDailyLimit
}

// coverTooSmall 读得到尺寸且小于本平台封面下限；读不到不拦，交给平台判断。
func (p yunsuoPlatformProfile) coverTooSmall(src string) (int, int, bool) {
	if p.CoverMinWidth <= 0 && p.CoverMinHeight <= 0 {
		return 0, 0, false
	}
	width, height, ok := yunsuoImageSize(src)
	return width, height, ok && (width < p.CoverMinWidth || height < p.CoverMinHeight)
}
