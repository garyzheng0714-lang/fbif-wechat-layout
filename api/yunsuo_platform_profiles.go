package main

// yunsuo_platform_profiles.go — 多平台分发的平台层。
//
// 2026-10-12 用户裁定：分发规则分两层，底层是各平台通用的（署名、去名片与二维码、
// 去链接、图注合并、文末声明、封面取图、标题截断，见 yunsuo_distribution_rules.go），
// 往下每个平台再有自己的一套。这里只写各平台与通用不同的参数；封面、配额、链接核对都按
// 账号所属平台取。加平台只加一份 profile，不改通用层。
//
// 稿子都经自己的分发服务（FBIF-多平台分发工具）写进平台编辑器、只存草稿不发布（百家号 2026-10-22、
// 头条号 2026-10-24 起；云梭同日下线）。正文标签写法（Markup）按各平台编辑器留得住的来（yunsuo_render.go）。

import "strings"

// externalDriver lane 上记的送稿方式：robot 是自己的分发服务；空是 2026-10-24 下线的云梭留下的旧 lane，只显示。
type externalDriver string

const externalDriverRobot externalDriver = "robot"

type yunsuoPlatformProfile struct {
	Key  string
	Name string
	// 封面下限，读得到图片尺寸时才拦；0 表示平台没有已知下限。
	CoverMinWidth, CoverMinHeight int
	// 标题原样送，TitleMin–TitleMax 是字数范围（按 titleLength 的数法）。
	TitleMin int
	TitleMax int
	// 标题里的英文字母、数字、半角符号两个算一个字（头条号）。
	TitleHalfWidthASCII bool
	// 摘要字数上限，推送前检查用；0 表示平台没有已知上限，不查。
	SummaryMax int
	// 平台每天的文章上限，自动外发按账号各自计数（人工按钮发的也计入）。
	DailyLimit int
	// 文章链接所在域名：人工登记已发链接、分发服务给的草稿地址都只认这些域名。
	URLHosts []string
	// 人工登记已发时怎么按标题找文章链接（publish_external_links.go）；空表示不自动找。
	LinkLookup string
	// 平台后台的内容管理页：没拿到文章或草稿链接时，卡片与侧边栏给这个链接看情况
	// （2026-10-13 用户：「百家号的已发布，我没看到任何的链接。我也查看不了发布情况」）。
	ManageURL string
	// 审核卡分发区平台块里的平台图标（api/cardicons/<Icon>.png，平台自己的网站图标）。
	Icon string
	// 正文标签写法。
	Markup distributionMarkup
}

const yunsuoDefaultDailyLimit = 5

var yunsuoPlatformProfiles = map[string]yunsuoPlatformProfile{
	// 头条号：封面由分发服务在封面弹窗里上传、不进正文。封面下限来自 2026-10-11 #183 平台回执；
	// 每天约 5 篇上限。
	"toutiao": {
		Key: "toutiao", Name: "头条号",
		Markup:        toutiaoEditorMarkup,
		CoverMinWidth: 452, CoverMinHeight: 352,
		// 标题 2–30 字，标点与空格都算（2026-10-13 用户裁定），英文字母与数字两个算一个字：
		// 编辑手发的 #287（32 个字符）、#292（35）、#289（42，按此数法正好 30）头条都收了。
		TitleMin: 2, TitleMax: 30, TitleHalfWidthASCII: true,
		DailyLimit: 5,
		URLHosts:   []string{"toutiao.com", "www.toutiao.com", "m.toutiao.com"},
		LinkLookup: externalLinkLookupToutiao,
		ManageURL:  "https://mp.toutiao.com/profile_v4/manage/content/all",
		Icon:       "platform-toutiao",
	},
	// 百家号：封面由分发服务在编辑器的封面弹窗里上传、不进正文，标题原样送（百家号 2–64 字）。
	// 不设封面下限。百度搜索有验证码，人工登记时文章链接不自动找。
	"baijiahao": {
		Key: "baijiahao", Name: "百家号",
		Markup:     baijiahaoEditorMarkup,
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

// yunsuoGenericProfile 认不出平台的账号（只会是旧 lane：不在平台规则里的号不会被挑上）：通用版式，
// 不设封面下限。
var yunsuoGenericProfile = yunsuoPlatformProfile{
	Key: "generic", Name: "外部平台",
	TitleMin: 2, TitleMax: 64,
	DailyLimit: yunsuoDefaultDailyLimit,
}

// titleLimits 这个平台标题的字数范围（按 titleLength 的数法）。
func (p yunsuoPlatformProfile) titleLimits() (int, int) {
	return p.TitleMin, p.TitleMax
}

// titleInputMax 标题输入框按字符数最多能填几个：英文字母、数字两个算一个字的平台（头条号），
// 全是半角时可到上限的两倍；超没超由服务端按平台数法判。
func (p yunsuoPlatformProfile) titleInputMax() int {
	if p.TitleHalfWidthASCII {
		return 2 * p.TitleMax
	}
	return p.TitleMax
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

// titleOver 标题超了平台上限吗（按平台的数法）：返回上限与数出的长度。
func (p yunsuoPlatformProfile) titleOver(title string) (length, max int, over bool) {
	_, max = p.titleLimits()
	length = p.titleLength(title)
	return length, max, length > max
}

func yunsuoProfileForTarget(target externalAccount) yunsuoPlatformProfile {
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

// syncVerb 卡片、侧边栏与提醒里「送去这个平台」怎么说：自己的分发服务目前只存草稿。
func (p yunsuoPlatformProfile) syncVerb() string { return "存草稿" }

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
