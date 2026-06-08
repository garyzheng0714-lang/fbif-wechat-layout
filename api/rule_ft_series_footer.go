package main

// rule_ftss_footer.go — 网站「什么是」系列商务合作底部版块（fbif_footer 页脚库覆盖）。
//
// 结构来源：8 篇真实 static.foodtalks.cn「什么是」成品尾部（2026-06-08 逐元素核对）。用网站
// 简洁 inline-style 口径表达成品内容（非 1:1 抄 lark 冗余 HTML），视觉对齐：商务合作标题 18px
// #01396a 加粗、正文 16px #333333、群备注 14px #7f7f7f、链接 #105b89 下划线、图 width=600。
// 图走 static.foodtalks.cn（网站渠道直接引用，transferMmbiz 只转 mmbiz）；BD 名片走站内素材。
// 素材定义见 rule_asset_library.go 的 defaultFtSeriesSiteCoopAssets。
//
// BD 名片：用户裁定「每篇默认放一张」——bd_card 段默认 Eileen；换 soso/tiki 由侧栏选择器切换
// （前端选择器待接入；切换前默认 Eileen 满足「每篇默认放一张」）。
// 「相关合作」表（公司策划/视频策划/食品雷达 3 行）暂未纳入——待表格 HTML 精修后补（见 WORKLOG）。
func defaultFtSeriesBizCoopFooterModule() map[string]any {
	seg := func(lines ...string) map[string]any {
		arr := make([]any, len(lines))
		for i, l := range lines {
			arr[i] = l
		}
		return map[string]any{"html_lines": arr}
	}
	return map[string]any{
		"enabled":          true,
		"active_preset_id": "ft-series-coop",
		"segment_order":    []any{"biz_coop", "bd_card", "join_group", "follow_account", "logo", "more_info"},
		"segments": map[string]any{
			"biz_coop": seg(
				`<p><strong><span style="font-size: 18px; color: #01396a;">什么是系列商务合作</span></strong></p>`,
				`<p style="text-align: justify;"><span style="font-size: 16px; color: #333333;">FoodTalks什么是系列创意内容策划，为食品企业提供深度、有价值、有故事性的专访创意策划服务。</span></p>`,
				`<p style="text-align: justify;"><span style="font-size: 16px; color: #333333;">什么是系列创意内容策划为食品企业提供创意名片，让用户通过一篇文章了解企业的实力！（详细请参考：<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/30894" target="_blank" rel="noopener">合作方案</a> &amp; <a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/32980" target="_blank" rel="noopener">合作案例</a>）</span></p>`,
				`<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ftss_coop_card")+`" alt="" width="600" /></p>`,
				`<p style="text-align: justify;"><span style="font-size: 16px; color: #333333;">更多合作，请点击：<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/31065" target="_blank" rel="noopener">FBIF &amp; FoodTalks系列商务合作方案</a></span></p>`,
			),
			"bd_card": seg(
				`<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="` + assetRefID("ftss_bd_eileen") + `" alt="BD名片" width="600" /></p>`,
			),
			"join_group": seg(
				`<p style="text-align: justify;"><span style="font-size: 16px; color: #333333;">加入FBIF功能性食品群</span></p>`,
				`<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ftss_group_qr")+`" alt="" width="600" /></p>`,
				`<p style="text-align: justify;"><span style="font-size: 14px; color: #7f7f7f;">（添加请备注：姓名 公司 职位）</span></p>`,
			),
			"follow_account": seg(
				`<p style="text-align: justify;"><span style="font-size: 16px; color: #333333;">关注FoodTalks什么是系列公众号</span></p>`,
				`<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ftss_account_qr")+`" alt="" width="600" /></p>`,
			),
			"logo": seg(
				`<p><img style="max-width: 100%; height: 27px; width: 126px;" src="` + assetRefID("ft_footer_logo") + `" alt="FoodTalks logo" width="130" height="27" border="0" /></p>`,
			),
			"more_info": seg(
				`<p>点击获取更多<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/" target="_blank" rel="noopener">全球食品资讯</a></p>`,
			),
		},
		"presets": []any{
			map[string]any{"id": "ft-series-coop", "name": "什么是系列商务合作（完整）", "segments": map[string]any{"biz_coop": true, "bd_card": true, "join_group": true, "follow_account": true, "logo": true, "more_info": true}},
			map[string]any{"id": "none", "name": "无页脚", "segments": map[string]any{"biz_coop": false, "bd_card": false, "join_group": false, "follow_account": false, "logo": false, "more_info": false}},
		},
	}
}
