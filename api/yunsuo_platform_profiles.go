package main

// yunsuo_platform_profiles.go — 多平台分发的平台层。
//
// 2026-10-12 用户裁定：分发规则分两层，底层是各平台通用的（署名、去名片与二维码、
// 去链接、图注合并、文末声明、封面取图、标题截断，见 yunsuo_distribution_rules.go），
// 往下每个平台再有自己的一套。这里只写各平台与通用不同的参数；封面、配额、链接核对都按
// 账号所属平台取。加平台只加一份 profile，不改通用层。
//
// 正文 HTML 各平台同一份：推给云梭的正文会被它重写成各平台自己的格式，平台原生写法
// （头条的 h1.pgc-h-forward-slash、pgc-img-caption）进去就丢（2026-10-20 实测），所以
// 这里没有任何 HTML 参数，正文只按云梭吃得进去的写法渲染一次（yunsuo_render.go）。

import "strings"

type yunsuoPlatformProfile struct {
	Key  string
	Name string
	// 云梭会把 cover 插到这个平台正文的最前面（百家号：#95 8 月稿、#282 都是）。这样的平台，
	// 文章以图开头就用这张图当封面并从正文拿掉，插回来正好在原位。头条号用公众号封面时不插
	// （10-10～10-12 #165 #175 #180 #193 #196 #260），正文不动。
	CoverPrepended bool
	// 封面下限，读得到图片尺寸时才拦；0 表示平台没有已知下限。
	CoverMinWidth, CoverMinHeight int
	// 云梭按平台在三档标题里挑一档（yunsuoTitleShort/Medium/Long）；人改过的平台标题原样
	// 写进这一档。TitleMax 是推送前检查用的字数上限，0 表示按这一档的上限。
	TitleField string
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
}

const yunsuoDefaultDailyLimit = 5

var yunsuoPlatformProfiles = map[string]yunsuoPlatformProfile{
	// 头条号：封面下限来自 2026-10-11 #183 平台回执；每天约 5 篇上限。
	"toutiao": {
		Key: "toutiao", Name: "头条号",
		CoverMinWidth: 452, CoverMinHeight: 352,
		// 标题最多 30 字，标点与空格都算（2026-10-13 用户裁定），英文字母与数字两个算一个字：
		// 编辑手发的 #287（32 个字符）、#292（35）、#289（42，按此数法正好 30）头条都收了。
		// 云梭给头条号用的是「标题」档：线上头条稿标题都是 title 原文（#196、#260），不是短标题。
		TitleField: yunsuoTitleMedium, TitleMax: 30, TitleHalfWidthASCII: true,
		DailyLimit:   5,
		URLHosts:     []string{"toutiao.com", "www.toutiao.com", "m.toutiao.com"},
		LinkLookup:   externalLinkLookupToutiao,
		ContentFetch: externalFidelityFetchToutiao,
		ManageURL:    "https://mp.toutiao.com/profile_v4/manage/content/all",
		Icon:         "platform-toutiao",
	},
	// 百家号：不设封面下限。百度搜索有验证码，文章链接不自动找、线上正文不取，由管理员登记。
	"baijiahao": {
		Key: "baijiahao", Name: "百家号",
		CoverPrepended: true,
		// 云梭给百家号用的是长标题档：线上百家号稿标题都是 long_title（#165 #175 #180 都是
		// 补成「原标题｜摘要」的那一档）。
		TitleField: yunsuoTitleLong,
		DailyLimit: yunsuoDefaultDailyLimit,
		URLHosts:   []string{"baijiahao.baidu.com", "mbd.baidu.com"},
		ManageURL:  "https://baijiahao.baidu.com/builder/rc/content",
		Icon:       "platform-baijiahao",
	},
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

// titleLimits 这个平台标题的字数范围：下限是云梭这一档的下限，上限取平台规则与这一档上限中较小的。
func (p yunsuoPlatformProfile) titleLimits() (int, int) {
	min, max := yunsuoTitleFieldRange(p.TitleField)
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

// titleOver 标题推得出去吗：先看云梭这一档按字符算的上限，再看平台自己的上限（按平台的数法）。
// 超了返回超的那条上限与按它数出的长度。
func (p yunsuoPlatformProfile) titleOver(title string) (length, max int, over bool) {
	_, fieldMax := yunsuoTitleFieldRange(p.TitleField)
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
