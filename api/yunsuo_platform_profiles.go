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
	// 平台每天的文章上限，自动外发按账号各自计数（人工按钮发的也计入）。
	DailyLimit int
	// 文章链接所在域名：人工登记已发链接时只认这些域名。
	URLHosts []string
	// 推送成功后怎么找文章链接（publish_external_links.go）；空表示不自动找。
	LinkLookup string
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
		DailyLimit: 5,
		URLHosts:   []string{"toutiao.com", "www.toutiao.com", "m.toutiao.com"},
		LinkLookup: externalLinkLookupToutiao,
	},
	// 百家号：还没有人工样稿可比对，版式暂与头条号相同、不设封面下限；拿到样稿后
	// 只改这里。百度搜索有验证码，文章链接不自动找，由管理员登记。
	"baijiahao": {
		Key: "baijiahao", Name: "百家号",
		HeadingOpen: `<h1 class="pgc-h-forward-slash">`, HeadingClose: "</h1>",
		NativeCaption: true,
		DailyLimit:    yunsuoDefaultDailyLimit,
		URLHosts:      []string{"baijiahao.baidu.com", "mbd.baidu.com"},
	},
}

// yunsuoGenericProfile 认不出平台的账号：通用版式，不设封面下限。
var yunsuoGenericProfile = yunsuoPlatformProfile{
	Key: "generic", Name: "外部平台",
	HeadingOpen: `<h1 class="pgc-h-forward-slash">`, HeadingClose: "</h1>",
	NativeCaption: true,
	DailyLimit:    yunsuoDefaultDailyLimit,
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
