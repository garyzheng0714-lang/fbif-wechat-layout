package main

// yunsuo_platform_profiles.go — 多平台分发的平台层。
//
// 2026-10-12 用户裁定：分发规则分两层，底层是各平台通用的（署名、去名片与二维码、
// 去链接、图注合并、文末声明、封面取图、标题截断，见 yunsuo_distribution_rules.go），
// 往下每个平台再有自己的一套。这里只写各平台与通用不同的参数；渲染、封面、配额、
// 链接核对都按账号所属平台取。加平台只加一份 profile，不改通用层。

import "strings"

type yunsuoPlatformProfile struct {
	Key  string
	Name string
	// 网站标题1（level≤2）写成什么；标题2 一律加粗段。
	HeadingOpen, HeadingClose string
	// 有图注的图：true 用平台原生图片描述（pgc-img），false 图下另起一段。
	NativeCaption bool
	// 封面下限，读得到图片尺寸时才拦；0 表示平台没有已知下限。
	CoverMinWidth, CoverMinHeight int
	// 云梭按平台在三档标题里挑一档（yunsuoTitleShort/Medium/Long）；人改过的平台标题原样
	// 写进这一档。TitleMax 是推送前检查用的字数上限，0 表示按这一档的上限。
	TitleField string
	TitleMax   int
	// 摘要字数上限，推送前检查用；0 表示平台没有已知上限，不查。
	SummaryMax int
	// 平台每天的文章上限，自动外发按账号各自计数（人工按钮发的也计入）。
	DailyLimit int
	// 文章链接所在域名：人工登记已发链接时只认这些域名。
	URLHosts []string
	// 推送成功后怎么找文章链接（publish_external_links.go）；空表示不自动找。
	LinkLookup string
	// 平台后台的内容管理页：已发布但还没拿到文章链接时，卡片与侧边栏给这个链接看发布情况
	// （2026-10-13 用户：「百家号的已发布，我没看到任何的链接。我也查看不了发布情况」）。
	ManageURL string
	// 审核卡分发区平台块里的平台图标（api/cardicons/<Icon>.png，平台自己的网站图标）。
	Icon string
}

const yunsuoDefaultDailyLimit = 5

var yunsuoPlatformProfiles = map[string]yunsuoPlatformProfile{
	// 头条号：金标准是 2026-03～08 人工发在「FBIF食品饮料创新」上的 16 篇稿。正文只认 h1 作
	// 小标题（h2 会被压成普通段落）；封面下限来自 2026-10-11 #183 平台回执；每天约 5 篇上限。
	"toutiao": {
		Key: "toutiao", Name: "头条号",
		HeadingOpen: `<h1 class="pgc-h-forward-slash">`, HeadingClose: "</h1>",
		NativeCaption: true,
		CoverMinWidth: 452, CoverMinHeight: 352,
		// 标题最多 30 字，标点与空格都算（2026-10-13 用户裁定）。云梭给头条号用的是「标题」档：
		// 线上 7 篇头条稿标题都是 title 原文（#196 27 字、#260 32 字），不是 8–20 字的短标题。
		TitleField: yunsuoTitleMedium, TitleMax: 30,
		DailyLimit: 5,
		URLHosts:   []string{"toutiao.com", "www.toutiao.com", "m.toutiao.com"},
		LinkLookup: externalLinkLookupToutiao,
		ManageURL:  "https://mp.toutiao.com/profile_v4/manage/content/all",
		Icon:       "platform-toutiao",
	},
	// 百家号：还没有人工样稿可比对，版式暂与头条号相同、不设封面下限；拿到样稿后
	// 只改这里。百度搜索有验证码，文章链接不自动找，由管理员登记。
	"baijiahao": {
		Key: "baijiahao", Name: "百家号",
		HeadingOpen: `<h1 class="pgc-h-forward-slash">`, HeadingClose: "</h1>",
		NativeCaption: true,
		// 还没有一篇推成功过，用的哪一档标题未知，先按云梭通用标题（≤40 字）查。
		TitleField: yunsuoTitleMedium,
		DailyLimit: yunsuoDefaultDailyLimit,
		URLHosts:   []string{"baijiahao.baidu.com", "mbd.baidu.com"},
		ManageURL:  "https://baijiahao.baidu.com/builder/rc/content",
		Icon:       "platform-baijiahao",
	},
}

// yunsuoGenericProfile 认不出平台的账号：通用版式，不设封面下限。
var yunsuoGenericProfile = yunsuoPlatformProfile{
	Key: "generic", Name: "外部平台",
	HeadingOpen: `<h1 class="pgc-h-forward-slash">`, HeadingClose: "</h1>",
	NativeCaption: true,
	TitleField:    yunsuoTitleMedium,
	DailyLimit:    yunsuoDefaultDailyLimit,
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
