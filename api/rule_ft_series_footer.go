package main

// rule_ftss_footer.go — 网站「什么是」系列商务合作底部版块（fbif_footer 页脚库覆盖）。
//
// 结构来源：8 篇真实 static.foodtalks.cn「什么是」成品尾部（2026-06-08 逐元素核对）。用网站
// 简洁 inline-style 口径表达成品内容（非 1:1 抄 lark 冗余 HTML），视觉对齐：商务合作标题 18px
// #01396a 加粗、正文 16px #333333、群备注 14px #7f7f7f、链接 #105b89 下划线、图 width=600。
// 图走 static.foodtalks.cn（网站渠道直接引用，transferMmbiz 只转 mmbiz）；BD 名片走站内素材。
// 素材定义见 rule_asset_library.go 的 defaultFtSeriesSiteCoopAssets。
//
// BD 名片：用户裁定「每篇默认放一张」——bd_card 段默认使用橙宝成品页对应的 Soso 名片；
// 后续若要按文章/BD 自动切换，应继续走素材库引用，不把图片 URL 硬写到渲染器。
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
		"segment_order":    []any{"biz_coop", "bd_card", "coop_table", "join_group", "follow_account", "logo", "more_info"},
		"segments": map[string]any{
			"biz_coop": seg(
				`<p><strong><span style="font-size: 18px; color: #01396a;">什么是系列商务合作</span></strong></p>`,
				`<p style="text-align: justify;"><span style="font-size: 16px; color: #333333;">FoodTalks什么是系列公司内容策划帮助食品企业更清晰、更完整、更通俗易懂、更有趣地呈现企业信息。</span></p>`,
				`<p><br></p>`,
				`<p style="text-align: justify;"><span style="font-size: 16px; color: #333333;">什么是系列公司内容策划打造企业<strong>线上名片</strong>，让用户通过一篇文章了解企业的全部！</span><span style="font-size: 16px; color: #333333;">（详细请参考：<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/30894" target="_blank" rel="noopener">合作方案</a> &amp; <a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/32980" target="_blank" rel="noopener">合作案例</a>）</span></p>`,
				`<p><br></p>`,
			),
			"bd_card": seg(
				`<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ftss_bd_soso")+`" alt="BD名片" width="600" height="175" /></p>`,
				`<p><br></p>`,
			),
			"coop_table": seg(
				`<p><span style="font-size: 18px; color: #01396a;"><strong>相关合作：</strong></span></p>`,
				`<table style="border-collapse: collapse; width: 100%;"><tbody><tr><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; background-color: #01396a; color: #ffffff; font-size: 16px;"><strong>项目</strong></td><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; background-color: #01396a; color: #ffffff; font-size: 16px;"><strong>介绍</strong></td><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; background-color: #01396a; color: #ffffff; font-size: 16px;"><strong>合作方案</strong></td></tr><tr><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; color: #333333; font-size: 16px;">FoodTalks<br><strong>什么是系列公司策划</strong></td><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; color: #333333; font-size: 14px;"><strong>什么是系列，打造线上的企业名片</strong></td><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; font-size: 16px;"><a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/30894" target="_blank" rel="noopener">合作方案</a> <span style="color: #333333;">|</span> <a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/32980" target="_blank" rel="noopener">300+什么是汇总</a></td></tr><tr><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; color: #333333; font-size: 16px;">FBIF &amp; FoodTalks<br><strong>视频策划</strong></td><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; color: #333333; font-size: 14px;"><strong>视频策划，打造企业视频名片</strong></td><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; font-size: 16px;"><a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/39133" target="_blank" rel="noopener">合作方案</a></td></tr><tr><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; color: #333333; font-size: 16px;">FoodTalks<br><strong>食品雷达</strong></td><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; color: #333333; font-size: 14px;">已收录<strong>6000+</strong>家优质企业</td><td style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; font-size: 16px;"><a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/30826" target="_blank" rel="noopener">入驻方案</a></td></tr><tr><td colspan="3" style="border: 1px solid #7f7f7f; padding: 8px; text-align: center; color: #333333; font-size: 16px;">更多合作，请点击：<br><a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/31065" target="_blank" rel="noopener">FBIF &amp; FoodTalks系列商务合作方案</a></td></tr></tbody></table>`,
				`<p><br></p>`,
			),
			"join_group": seg(
				`<p><span style="font-size: 18px; color: #01396a;"><strong>加入FBIF饮料群</strong></span></p>`,
				`<p><img class="mc-img" style="max-width: 100%; height: auto; display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ftss_group_qr")+`" alt="lemon黑白码" width="150" height="150" /></p>`,
				`<p style="text-align: justify;"><span style="font-size: 14px; color: #7f7f7f;">（添加请备注：姓名 公司 职位）</span></p>`,
			),
			"follow_account": seg(
				`<p><span style="font-size: 18px; color: #01396a;"><strong>关注FoodTalks什么是系列公众号</strong></span></p>`,
				`<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="`+assetRefID("ftss_account_qr")+`" alt="" width="600" height="180" /></p>`,
			),
			"logo": seg(
				`<p><img style="max-width: 100%; height: 27px; width: 126px;" src="` + assetRefID("ft_footer_logo") + `" alt="FoodTalks logo" width="130" height="27" border="0" /></p>`,
			),
			"more_info": seg(
				`<p>点击获取更多<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/" target="_blank" rel="noopener">全球食品资讯</a></p>`,
			),
		},
		"presets": []any{
			map[string]any{"id": "ft-series-coop", "name": "什么是系列商务合作（完整）", "segments": map[string]any{"tip": false, "biz_coop": true, "bd_card": true, "coop_table": true, "join_group": true, "follow_account": true, "logo": true, "more_info": true}},
			map[string]any{"id": "none", "name": "无页脚", "segments": map[string]any{"tip": false, "biz_coop": false, "bd_card": false, "coop_table": false, "join_group": false, "follow_account": false, "logo": false, "more_info": false}},
		},
	}
}
