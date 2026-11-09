package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func init() {
	// 测试默认读不到图片尺寸（不出网）；要尺寸的用例用 stubDistributionImageSize 显式给。
	distributionImageSize = func(string) (int, int, bool) { return 0, 0, false }
}

func stubDistributionImageSize(t *testing.T, sizes map[string][2]int) {
	t.Helper()
	previous := distributionImageSize
	distributionImageSize = func(src string) (int, int, bool) {
		size, ok := sizes[src]
		return size[0], size[1], ok
	}
	t.Cleanup(func() { distributionImageSize = previous })
}

// 正文块里只留文字与加粗：链接只留锚文字、脚本照字面留着（写成标签时由分发服务转义）；
// 视频地址没转存就放封面图；文末补转载声明。
func TestDistributionBlocksDropLinksAndScripts(t *testing.T) {
	blocks := `[
		{"k":"h","level":2,"text":"章节 <一>"},
		{"k":"p","runs":[{"type":"txt","text":"正文 & ","bold":true},{"type":"link","text":"不安全链接","href":"javascript:alert(1)"}]},
		{"k":"img","src":"https://origin.example/image.jpg"},
		{"k":"li","runs":[{"type":"txt","text":"第一项"}]},
		{"k":"li","runs":[{"type":"link","text":"第二项","href":"https://example.com/?a=1&b=2"}]},
		{"k":"table","rows":[[{"header":true,"runs":[{"type":"txt","text":"表头"}]}],[{"runs":[{"type":"txt","text":"<script>"}]}]]},
		{"k":"video","src":"https://cdn.example/video.mp4","poster":"https://origin.example/poster.jpg"}
	]`
	imageMap := `{"https://origin.example/image.jpg":"https://cdn.example/image.jpg","https://origin.example/poster.jpg":"https://cdn.example/poster.jpg"}`
	want := strings.Join([]string{
		"h2 章节 <一>",
		"txt **正文 & **不安全链接",
		"img https://cdn.example/image.jpg",
		"li 第一项",
		"li 第二项",
		"table [表头] / <script>",
		"img https://cdn.example/poster.jpg",
		"txt 本文为FBIF食品饮料创新原创，转载请联系授权。",
	}, "\n")
	if got := outgoingOutline(t, blocks, imageMap); got != want {
		t.Fatalf("外发正文块：\n%s\n想要：\n%s", got, want)
	}

	job := publishJob{
		Title:      "新品趋势",
		SourceName: "FBIF食品饮料创新",
		BlocksJSON: blocks,
		ImageMap:   imageMap,
		CoverURL:   "https://origin.example/image.jpg",
	}
	payload, err := buildRobotDraftPayload(job, testPlatform(t, "toutiao"), publishExternalDraft{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(payload.Blocks)
	if payload.CoverURL != "https://cdn.example/image.jpg" || payload.Title != job.Title || strings.Contains(string(raw), "javascript:") || strings.Contains(string(raw), "href") {
		t.Fatalf("草稿内容不符合预期：%#v", payload)
	}
}

// manualLayoutTestJob 照编辑手发稿拼的一篇：署名、带图注的首图、链接、整段加粗、两级小标题、
// 作者名片与参考来源。
func manualLayoutTestJob() publishJob {
	blocks := `[
		{"k":"txt","text":"作者：Sage，编辑：Bobo。"},
		{"k":"txt","blankBefore":true,"text":"一个人，可以在同一赛道成功2次吗？"},
		{"k":"img","blankBefore":true,"src":"https://cdn.example/lead.jpg"},
		{"k":"cap","text":"蛋白棒RXBAR和David"},
		{"k":"cap","text":"图片来源：RXBAR、David"},
		{"k":"txt","blankBefore":true,"runs":[{"type":"txt","text":"例如我们曾介绍过的Poppi等。https://mp.weixin.qq.com/s/M_dPjnJED8Xx4JJw_3lQ6g同样是主打干净的产品，"},{"type":"txt","text":"RXBAR却没选这条路。","bold":true}]},
		{"k":"txt","runs":[{"type":"txt","text":"整段加粗的导语。","bold":true}]},
		{"k":"h","level":2,"text":"第一次创业，他把蛋白棒做简单"},
		{"k":"txt","runs":[{"type":"txt","text":"这是我"},{"type":"link","text":"茶叶系列","href":"https://www.foodtalks.cn/news/tag/114000"},{"type":"txt","text":"的第38篇。"}]},
		{"k":"img","src":"https://cdn.example/photo.png"},
		{"k":"cap","text":"Peter Rahal在2024年推出蛋白质品牌David，主打每根28克蛋白质、只有150卡路里的蛋白棒"},
		{"k":"cap","text":"图片来源：Forbes"},
		{"k":"h","level":3,"text":"1、包装更新"},
		{"k":"img","src":"https://cdn.example/wide-but-captioned.png"},
		{"k":"cap","text":"三得利伊右卫门绿茶全家福"},
		{"k":"img","src":"https://cdn.example/photo.png"},
		{"k":"cap","text":"真的！是西柚味！"},
		{"k":"cap","text":"图片来源：X@木本牙狼"},
		{"k":"h","level":2,"text":"结语"},
		{"k":"txt","text":"下一篇，我继续写中国茶叶品牌。"},
		{"k":"img","blankBefore":true,"src":"https://cdn.example/mote-card.png"},
		{"k":"txt","blankBefore":true,"text":"参考来源："},
		{"k":"txt","text":"[1] The RXBAR Story"},
		{"k":"img","blankBefore":true,"src":"https://cdn.example/sage-card.png"}
	]`
	imageMap := `{"https://cdn.example/lead.jpg":"https://cdn.example/lead.jpg","https://cdn.example/photo.png":"https://cdn.example/photo.png",
		"https://cdn.example/wide-but-captioned.png":"https://cdn.example/wide-but-captioned.png",
		"https://cdn.example/mote-card.png":"https://cdn.example/mote-card.png","https://cdn.example/sage-card.png":"https://cdn.example/sage-card.png",
		"https://cdn.example/wechat-cover.jpg":"https://cdn.example/wechat-cover.jpg"}`
	return publishJob{
		Title:      "估值超151亿元！蛋白棒赛道创业，他连赢了两次",
		SourceName: "FBIF食品饮料创新",
		Summary:    "竞业协议到期后，明星创业者重走“老路”。",
		CoverURL:   "https://cdn.example/wechat-cover.jpg",
		BlocksJSON: blocks,
		ImageMap:   imageMap,
	}
}

// 外发版式的金标准是编辑手发在头条号、百家号上的稿（2026-10-20 用户：「按照人工稿改，这应该是
// 你的标准」；对照 10-15 起的 #286 #287 #289 #290 #292 #299 与 8 月老稿）。各平台的正文规则同一套
// （开头不放署名、编号、去名片、无链接、文末声明），排版工具全落进正文块；块写成哪个平台的什么标签归分发服务。
// 首图留在原位，封面是公众号封面、单独上传不进正文，标题原样。
func TestDistributionBlocksFollowManualDrafts(t *testing.T) {
	stubDistributionImageSize(t, nil)
	job := manualLayoutTestJob()
	want := strings.Join([]string{
		"txt 一个人，可以在同一赛道成功2次吗？",
		"img https://cdn.example/lead.jpg",
		"cap 蛋白棒RXBAR和David；图片来源：RXBAR、David",
		"txt 例如我们曾介绍过的Poppi等。同样是主打干净的产品，**RXBAR却没选这条路。**",
		"txt **整段加粗的导语。**",
		"h2 一、第一次创业，他把蛋白棒做简单",
		"txt 这是我茶叶系列的第38篇。",
		"img https://cdn.example/photo.png",
		"cap Peter Rahal在2024年推出蛋白质品牌David，主打每根28克蛋白质、只有150卡路里的蛋白棒；图片来源：Forbes",
		"h3 1、包装更新",
		"img https://cdn.example/wide-but-captioned.png",
		"cap 三得利伊右卫门绿茶全家福",
		"img https://cdn.example/photo.png",
		"cap 真的！是西柚味！图片来源：X@木本牙狼",
		"h2 二、结语",
		"txt 下一篇，我继续写中国茶叶品牌。",
		"txt 参考来源：",
		"txt [1] The RXBAR Story",
		"txt 本文为FBIF食品饮料创新原创，作者：Sage，编辑：Bobo，转载请联系授权。",
	}, "\n")
	var first []Block
	for _, key := range []string{"toutiao", "baijiahao"} {
		payload, err := buildRobotDraftPayload(job, testPlatform(t, key), publishExternalDraft{})
		if err != nil {
			t.Fatal(err)
		}
		if got := distributionOutline(payload.Blocks); got != want {
			t.Fatalf("%s外发正文块：\n%s\n想要：\n%s", key, got, want)
		}
		if first == nil {
			first = payload.Blocks
		} else if !reflect.DeepEqual(first, payload.Blocks) {
			t.Fatal("各平台的正文块应完全一样，写法差别归分发服务")
		}
		if payload.Platform != key || payload.CoverURL != "https://cdn.example/wechat-cover.jpg" || payload.Title != job.Title {
			t.Fatalf("%s封面用公众号封面、标题原样：%+v", key, payload)
		}
	}
}

// TestDistributionBlocksFollowContract 交给分发服务的块只有约定的几种、只带约定的字段（分发服务 docs/api.md「正文块」，
// 它收到多一个字段就 400）：样稿里 6 篇真稿和手发稿拼的那篇都查一遍。
func TestDistributionBlocksFollowContract(t *testing.T) {
	stubDistributionImageSize(t, nil)
	check := func(name string, blocks []Block) {
		t.Helper()
		allowed := map[string][]string{"h": {"k", "level", "text"}, "txt": {"k", "runs"}, "bq": {"k", "runs"}, "li": {"k", "runs"},
			"img": {"k", "src"}, "cap": {"k", "text"}, "table": {"k", "rows"}, "video": {"k", "src", "poster"}}
		for i, block := range blocks {
			raw, _ := json.Marshal(block)
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			for field := range fields {
				if !containsString(allowed[block.K], field) {
					t.Errorf("%s 第 %d 块（%s）多了字段 %s", name, i+1, block.K, field)
				}
			}
			switch {
			case block.K == "h" && block.Level != 2 && block.Level != 3:
				t.Errorf("%s 第 %d 块小标题级别是 %d", name, i+1, block.Level)
			case block.K == "cap" && (i == 0 || blocks[i-1].K != "img"):
				t.Errorf("%s 第 %d 块图注前面不是图", name, i+1)
			case block.K == "img" && !strings.HasPrefix(block.Src, "https://"):
				t.Errorf("%s 第 %d 块图片不是公网地址：%s", name, i+1, block.Src)
			}
			for _, run := range block.Runs {
				if run.Type != "txt" || run.Href != "" || run.Text == "" {
					t.Errorf("%s 第 %d 块有不是纯文字的片段：%+v", name, i+1, run)
				}
				// 片段只有文字和加粗：原文字色、字号、斜体都不外发（2026-11-06 用户：「头条号、百家号外发也去掉原文字色」）。
				raw, _ := json.Marshal(run)
				var runFields map[string]any
				_ = json.Unmarshal(raw, &runFields)
				for field := range runFields {
					if !containsString([]string{"type", "text", "bold"}, field) {
						t.Errorf("%s 第 %d 块的片段多了 %s：%+v", name, i+1, field, run)
					}
				}
			}
		}
	}
	colored := manualLayoutTestJob()
	colored.BlocksJSON = `[{"k":"txt","runs":[` +
		`{"type":"txt","text":"但是，她们亲眼看见。","bold":true,"color":"359ba3"},` +
		`{"type":"txt","text":"表内为单体数据。","color":"8f959e","italic":true,"fontSize":14}]},` +
		strings.TrimPrefix(strings.TrimSpace(colored.BlocksJSON), "[")
	coloredContent, err := buildDistributionContent(colored, testPlatform(t, "toutiao"), publishExternalDraft{})
	if err != nil {
		t.Fatal(err)
	}
	check("带原文字色的那篇", coloredContent.Blocks)
	if raw, _ := json.Marshal(coloredContent.Blocks); !strings.Contains(string(raw), `{"type":"txt","text":"但是，她们亲眼看见。","bold":true}`) {
		t.Errorf("彩色加粗的强调句要只留加粗：%s", raw)
	}
	content, err := buildDistributionContent(manualLayoutTestJob(), testPlatform(t, "toutiao"), publishExternalDraft{})
	if err != nil {
		t.Fatal(err)
	}
	check("手发稿拼的那篇", content.Blocks)
	for _, path := range distributionGoldenSamplePaths(t) {
		sample := loadDistributionGolden(t, path)
		got, err := distributionGoldenOutput(t, sample)
		if err != nil {
			t.Fatal(err)
		}
		check(path, got.Blocks)
	}
}

// 编译稿的「英文来源」「中文编辑」在正文最后（#289），人工稿把它们写进文末声明。
func TestDistributionMovesTailCreditsIntoStatement(t *testing.T) {
	stubDistributionImageSize(t, nil)
	blocks := `[
		{"k":"txt","text":"当地时间10月14日，Whole Foods Market发布2027年八大食品趋势。"},
		{"k":"txt","text":"Lentiful杯装即食扁豆餐以扁豆为主要原料。"},
		{"k":"txt","text":"英文来源：Whole Foods Market"},
		{"k":"txt","text":"中文编辑：kiki"}
	]`
	got := outgoingOutline(t, blocks, `{}`)
	if want := "txt Lentiful杯装即食扁豆餐以扁豆为主要原料。\ntxt 本文为FBIF食品饮料创新原创，英文来源：Whole Foods Market，中文编辑：kiki，转载请联系授权。"; !strings.HasSuffix(got, want) {
		t.Fatalf("文末署名应并进声明：%s", got)
	}
	// 没有署名就只写原创声明（#287）。
	if got = outgoingOutline(t, `[{"k":"txt","text":"十项趋势并不是一张需要同时完成的清单。"}]`, `{}`); !strings.HasSuffix(got, "txt 本文为FBIF食品饮料创新原创，转载请联系授权。") {
		t.Fatalf("无署名稿的声明不对：%s", got)
	}
}

// 小标题序号：人工稿两篇以上的标题1 都编「一、二、」；原文已经编了号（#292）、或只有一个标题
// （#290）就不动；参考来源不算章节。
func TestDistributionNumbersSectionHeadings(t *testing.T) {
	render := func(blocks string) string {
		t.Helper()
		return outgoingOutline(t, blocks, `{}`)
	}
	contains := func(got string, want ...string) bool {
		for _, line := range want {
			if !strings.Contains("\n"+got+"\n", "\n"+line+"\n") {
				return false
			}
		}
		return true
	}
	if got := render(`[{"k":"h","level":2,"text":"趋势一 营养设计"},{"k":"txt","text":"正文。"},{"k":"h","level":2,"text":"趋势二 精准蛋白"},{"k":"h","level":2,"text":"参考来源："},{"k":"txt","text":"[1] Innova"}]`); !contains(got, "h2 一、趋势一 营养设计", "h2 二、趋势二 精准蛋白", "h2 参考来源：") {
		t.Fatalf("两个以上标题1 应编号、参考来源不编：%s", got)
	}
	if got := render(`[{"k":"h","level":2,"text":"一、 一群做设计的人，“起义”做茶"},{"k":"h","level":2,"text":"被tea'stone的茶惊艳到"},{"k":"h","level":3,"text":"1. 山河瓶"}]`); !contains(got, "h2 一、 一群做设计的人，“起义”做茶", "h2 被tea'stone的茶惊艳到", "h3 1. 山河瓶") {
		t.Fatalf("原文已编号的不再编：%s", got)
	}
	if got := render(`[{"k":"h","level":2,"text":"炸薯片，还能把油换成水？"},{"k":"txt","text":"正文。"}]`); !contains(got, "h2 炸薯片，还能把油换成水？") {
		t.Fatalf("只有一个标题不编号：%s", got)
	}
	if got := render(`[{"k":"h","level":2,"text":"3.9元的瓶装茶"},{"k":"h","level":2,"text":"便利店的新货架"}]`); !contains(got, "h2 一、3.9元的瓶装茶") {
		t.Fatalf("小数开头不算已编号：%s", got)
	}
}

// 封面：在平台编辑器的封面弹窗里单独上传、不进正文，首图留在原位。
func TestDistributionCoverIsUploadedSeparately(t *testing.T) {
	stubDistributionImageSize(t, nil)
	blocks := `[
		{"k":"txt","text":"作者：Mote莫特"},
		{"k":"img","src":"https://cdn.example/lead.jpg"},
		{"k":"cap","text":"tea'stone深圳万象前海店"},
		{"k":"txt","text":"这是我茶叶系列的第39篇。"}
	]`
	job := publishJob{
		Title:      "最美的中国茶馆tea'stone，8年开了18家店：我的2周体验和观察",
		SourceName: "FBIF食品饮料创新",
		CoverURL:   "https://cdn.example/wechat-cover.jpg",
		BlocksJSON: blocks,
		ImageMap:   `{"https://cdn.example/lead.jpg":"https://cdn.example/lead.jpg","https://cdn.example/wechat-cover.jpg":"https://cdn.example/wechat-cover.jpg"}`,
	}
	for _, key := range []string{"toutiao", "baijiahao"} {
		payload, err := buildRobotDraftPayload(job, testPlatform(t, key), publishExternalDraft{})
		if err != nil {
			t.Fatal(err)
		}
		if got := distributionOutline(payload.Blocks); payload.CoverURL != "https://cdn.example/wechat-cover.jpg" ||
			!strings.HasPrefix(got, "img https://cdn.example/lead.jpg\ncap tea'stone深圳万象前海店\n") {
			t.Fatalf("%s：封面用公众号封面、单独上传，首图留在原位：cover=%q\n%s", key, payload.CoverURL, got)
		}
	}
}

// 标题用原文：标题原样送（百家号 2–64 字、头条号 2–30 字，数字照分发服务给的），不补「｜摘要」。
func TestDistributionTitleKeepsOriginal(t *testing.T) {
	stubDistributionImageSize(t, nil)
	job := publishJob{
		SourceName: "FBIF食品饮料创新",
		Summary:    "饮品越来越像甜品，背后的原料也有了更多值得探索的使用方式。",
		CoverURL:   "https://cdn.example/wechat-cover.jpg",
		BlocksJSON: `[{"k":"txt","text":"不管吃的喝的，现在都能喝了。"}]`,
		ImageMap:   `{"https://cdn.example/wechat-cover.jpg":"https://cdn.example/wechat-cover.jpg"}`,
	}
	toutiao, baijia := testPlatform(t, "toutiao"), testPlatform(t, "baijiahao")
	for _, title := range []string{"西柚汁原料价格翻倍，可果美开始用胡萝卜做“西柚汁”了", "巧克力也想成新茶饮的“心头好”"} {
		job.Title = title
		for _, profile := range []distributionPlatform{toutiao, baijia} {
			if payload, err := buildRobotDraftPayload(job, profile, publishExternalDraft{}); err != nil || payload.Title != title {
				t.Fatalf("%s标题原样送：%+v %v", profile.Name, payload, err)
			}
		}
	}
	job.Title = "茶"
	if _, err := buildRobotDraftPayload(job, baijia, publishExternalDraft{}); err == nil || !strings.Contains(err.Error(), "百家号标题要 2–64 字，现在 1 字") {
		t.Fatalf("百家号标题不到 2 字应拦：%v", err)
	}
}

// 头条号标题字数：英文字母、数字算半个字，2–30 字（与分发服务同一数法）。编辑手发的 #287（32 个字符）、
// #292（35）、#289（42）头条都收了；按每个字符算 1 会把它们误拦成待修改。
func TestToutiaoTitleCountsASCIIAsHalf(t *testing.T) {
	profile := testPlatform(t, "toutiao")
	for _, title := range []string{
		"Innova揭晓2027全球食品饮料十大趋势，穿越二十年创新周期",
		"最美的中国茶馆tea'stone，8年开了18家店：我的2周体验和观察",
		"Whole Foods Market发布2027年8大食品趋势！苦味升级、豆类热潮…",
	} {
		if issues := externalTextIssues(publishJob{Title: title}, profile, publishExternalDraft{}); len(issues) != 0 {
			t.Errorf("头条号收过的标题不应拦：%q %+v", title, issues)
		}
	}
	issues := externalTextIssues(publishJob{Title: "这个标题全是汉字一共三十一个字远远超过了头条号的标题字数上限啊"}, profile, publishExternalDraft{})
	if len(issues) != 1 || issues[0].Length != 31 || issues[0].Max != 30 {
		t.Fatalf("31 个汉字应超头条 30 字：%+v", issues)
	}
	if got := profile.titleLength("Whole Foods Market发布2027年8大食品趋势！苦味升级、豆类热潮…"); got != 30 {
		t.Fatalf("头条字数 = %d，想要 30", got)
	}
	stubDistributionImageSize(t, nil)
	job := manualLayoutTestJob()
	job.Title = "这个标题全是汉字一共三十一个字远远超过了头条号的标题字数上限啊"
	if _, err := buildRobotDraftPayload(job, profile, publishExternalDraft{}); err == nil || !strings.Contains(err.Error(), "头条号标题要 2–30 字（英文字母、数字两个算一个字），现在 31 字") {
		t.Fatalf("存草稿前按头条的数法拦：%v", err)
	}
}

func TestDistributionDropsTrailingNameCardWithoutReferences(t *testing.T) {
	stubDistributionImageSize(t, map[string][2]int{
		"https://cdn.example/card.png":  {970, 283},
		"https://cdn.example/photo.png": {1080, 922},
	})
	render := func(last string) string {
		blocks := `[
			{"k":"txt","text":"作者：Mote莫特"},
			{"k":"img","src":"https://cdn.example/lead.png"},
			{"k":"txt","text":"正文第一段。"},
			{"k":"img","src":"` + last + `"}
		]`
		imageMap := `{"https://cdn.example/lead.png":"https://cdn.example/lead.png","` + last + `":"` + last + `"}`
		return outgoingOutline(t, blocks, imageMap)
	}
	if got := render("https://cdn.example/card.png"); strings.Contains(got, "card.png") ||
		!strings.HasSuffix(got, "txt 本文为FBIF食品饮料创新原创，作者：Mote莫特，转载请联系授权。") {
		t.Fatalf("文末无图注的宽条名片应去掉，声明署名取作者：%s", got)
	}
	if got := render("https://cdn.example/photo.png"); !strings.Contains(got, "photo.png") {
		t.Fatalf("文末普通配图不得误删：%s", got)
	}
}

// TestDistributionCoverRespectsPlatformMinimum 2026-10-11 #183 头条号回执「封面图尺寸过小780x332，
// 平台要求不得小于452x352」：存草稿之前就按分发服务给的封面下限查。
func TestDistributionCoverRespectsPlatformMinimum(t *testing.T) {
	blocks := `[
		{"k":"txt","text":"正文第一段，交代这篇文章要讲的品牌故事。"},
		{"k":"img","src":"https://cdn.example/lead.png"},
		{"k":"txt","text":"正文第二段。"}
	]`
	job := publishJob{
		Title:      "一款中国食品品牌如何用产品创新打开新的消费市场",
		SourceName: "FBIF食品饮料创新",
		Summary:    "食品品牌通过产品创新打开新的消费市场。",
		Tags:       []string{"食品", "创新"},
		BlocksJSON: blocks,
		ImageMap:   `{"https://cdn.example/lead.png":"https://cdn.example/lead.png","https://cdn.example/wx-cover.jpg":"https://cdn.example/wx-cover.jpg"}`,
		CoverURL:   "https://cdn.example/wx-cover.jpg",
	}
	toutiao := testPlatform(t, "toutiao")

	stubDistributionImageSize(t, map[string][2]int{
		"https://cdn.example/lead.png":     {1080, 720},
		"https://cdn.example/wx-cover.jpg": {1200, 510},
	})
	payload, err := buildRobotDraftPayload(job, toutiao, publishExternalDraft{})
	if err != nil {
		t.Fatal(err)
	}
	if got := distributionOutline(payload.Blocks); payload.CoverURL != "https://cdn.example/wx-cover.jpg" || !strings.Contains(got, "img https://cdn.example/lead.png") {
		t.Fatalf("封面用公众号封面，正文图留在原位：cover=%q\n%s", payload.CoverURL, got)
	}

	// 公众号封面太小（#183 780×332）：存了也会被平台拒，直接不存并说明原因。
	stubDistributionImageSize(t, map[string][2]int{
		"https://cdn.example/lead.png":     {1080, 720},
		"https://cdn.example/wx-cover.jpg": {780, 332},
	})
	if _, err = buildRobotDraftPayload(job, toutiao, publishExternalDraft{}); err == nil || !strings.Contains(err.Error(), "780x332") {
		t.Fatalf("封面不够头条下限应拒绝存草稿并给出尺寸，实际 err=%v", err)
	}

	// 没有公众号封面才用正文第一张够大的图，正文照旧不动。
	job.CoverURL = ""
	stubDistributionImageSize(t, map[string][2]int{"https://cdn.example/lead.png": {1080, 720}})
	if payload, err = buildRobotDraftPayload(job, toutiao, publishExternalDraft{}); err != nil ||
		payload.CoverURL != "https://cdn.example/lead.png" || !strings.Contains(distributionOutline(payload.Blocks), "img https://cdn.example/lead.png") {
		t.Fatalf("没有公众号封面时用正文第一张图：cover=%q err=%v", payload.CoverURL, err)
	}
}

// 卡片、改稿页给的缩短建议在标点处断开（2026-10-12 头条号后台实拍：硬截 20 字，标题停在半个词上）。
func TestTitleTruncationBreaksAtPunctuation(t *testing.T) {
	cases := []struct{ title, want string }{
		{"三得利卖出100亿瓶绿茶，背后藏着一家236年的老茶铺", "三得利卖出100亿瓶绿茶"},
		{"估值超151亿元！蛋白棒赛道创业，他连赢了两次", "估值超151亿元！蛋白棒赛道创业"},
		{"英国销售额第一的蛋白棒，亿滋CEO夸它“比美国所有蛋白棒都好吃”", "英国销售额第一的蛋白棒"},
		{"元气森林推出天真白日梦气泡水；古茗上线HPP瓶装冷萃茶 | 创新周报", "元气森林推出天真白日梦气泡水"},
		{"这家300年日本老茶铺，为何成了全球最难抢的抹茶品牌？", "这家300年日本老茶铺"},
		{"把茶做成零食，这家老茶铺只开6家店", "把茶做成零食，这家老茶铺只开6家店"},
		// 断点在引号里会留下半边引号，宁可硬截。
		{"新品“轻负担零添加高蛋白，好喝不胖还便宜”", "新品“轻负担零添加高蛋白，好喝不胖还便宜"},
		// 唯一的断点前不足 8 字：硬截。
		{"蛋白棒，亿滋首席执行官称赞这是全英国最好吃的蛋白棒", "蛋白棒，亿滋首席执行官称赞这是全英国最好"},
	}
	for _, tc := range cases {
		if got := truncateTitleAtPause(tc.title, 8, 20); got != tc.want {
			t.Errorf("缩短(%q) = %q，想要 %q", tc.title, got, tc.want)
		}
	}
}

func TestDistributionBlocksDropUnmirroredAndNonPublicURLs(t *testing.T) {
	unsafeURLs := []string{
		"http://localhost/image.jpg",
		"http://127.0.0.1/image.jpg",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/image.jpg",
		"http://172.16.0.1/image.jpg",
		"http://192.168.1.1/image.jpg",
		"https://example.com:8443/image.jpg",
	}
	blocks := make([]map[string]any, 0, len(unsafeURLs)*2+2)
	imageMap := map[string]string{
		"https://origin.example/cover.jpg": "https://cdn.example/cover.jpg",
	}
	for i, unsafeURL := range unsafeURLs {
		imageKey := "https://origin.example/unsafe-" + string(rune('a'+i)) + ".jpg"
		imageMap[imageKey] = unsafeURL
		blocks = append(blocks,
			map[string]any{"k": "img", "src": imageKey},
			map[string]any{"k": "p", "runs": []map[string]any{{"type": "link", "text": "不安全链接", "href": unsafeURL}}},
		)
	}
	// 没出现在 ImageMap key/value 中的公网图同样必须丢弃：只送转存过的图，不让分发服务替我们回源。
	blocks = append(blocks,
		map[string]any{"k": "img", "src": "https://unmirrored.example/image.jpg"},
		map[string]any{"k": "img", "src": "https://origin.example/cover.jpg"},
	)
	blocksJSON, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	imageMapJSON, err := json.Marshal(imageMap)
	if err != nil {
		t.Fatal(err)
	}
	got := outgoingOutline(t, string(blocksJSON), string(imageMapJSON))
	if !strings.Contains(got, "img https://cdn.example/cover.jpg") {
		t.Fatalf("唯一合规的图应留在正文：%s", got)
	}
	if strings.Contains(got, "unmirrored.example") {
		t.Fatalf("未转存的图不得进入外发正文：%s", got)
	}
	for _, unsafeURL := range unsafeURLs {
		if strings.Contains(got, unsafeURL) {
			t.Fatalf("非公网 URL 不得进入外发正文：%s", unsafeURL)
		}
	}

	job := publishJob{
		Title:      "安全分发的原创文章",
		Summary:    "测试外部分发中的媒体和链接地址策略。",
		BlocksJSON: string(blocksJSON),
		ImageMap:   string(imageMapJSON),
		CoverURL:   "https://cdn.example/cover.jpg", // 已是 ImageMap value，允许。
	}
	payload, err := buildRobotDraftPayload(job, testPlatform(t, "toutiao"), publishExternalDraft{})
	if err != nil {
		t.Fatal(err)
	}
	if payload.CoverURL != "https://cdn.example/cover.jpg" || distributionOutline(payload.Blocks) != got {
		t.Fatalf("草稿只用转存过的公网图：cover=%q\n%s", payload.CoverURL, distributionOutline(payload.Blocks))
	}
}

func TestDistributionStripsWechatTrailer(t *testing.T) {
	blocks := `[
		{"k":"txt","runs":[{"type":"txt","text":"把家乡一度被低估的食物卖到英国第一。"}]},
		{"k":"txt","text":"参考来源："},
		{"k":"txt","text":"[1] Food Manufacture"},
		{"k":"txt","text":"提示："},
		{"k":"txt","text":"* 本文为FBIF原创，欢迎转发朋友圈；"},
		{"k":"cap","text":"/ 更多文章 /"},
		{"k":"img","src":"https://origin.example/card.jpg"},
		{"k":"txt","text":"预览时标签不可点"}
	]`
	imageMap := `{"https://origin.example/card.jpg":"https://cdn.example/card.jpg","https://cdn.example/cover.jpg":"https://cdn.example/cover.jpg"}`
	got := outgoingOutline(t, blocks, imageMap)
	if !strings.Contains(got, "英国第一") || !strings.Contains(got, "参考来源") {
		t.Fatalf("正文应保留：%s", got)
	}
	if leak := publishHTMLWechatTrailerLeak(got); leak != "" {
		t.Fatalf("外发正文仍含导流尾 %s：%s", leak, got)
	}
	if strings.Contains(got, "cdn.example/card.jpg") {
		t.Fatalf("作者名片图应随导流尾切除：%s", got)
	}

	payload, err := buildRobotDraftPayload(publishJob{
		Title:      "印尼博士创业，把家乡穷人食物卖成英国第一品牌",
		SourceName: "FBIF食品饮料创新",
		Summary:    "Better Nature 把天贝卖到英国品类第一。",
		CoverURL:   "https://cdn.example/cover.jpg",
		BlocksJSON: blocks,
		ImageMap:   imageMap,
	}, testPlatform(t, "baijiahao"), publishExternalDraft{})
	if err != nil {
		t.Fatal(err)
	}
	if leak := publishHTMLWechatTrailerLeak(distributionBlocksText(payload.Blocks)); leak != "" {
		t.Fatalf("冻结的草稿仍含导流尾 %s", leak)
	}
	// 用公众号封面；不得把导流尾里的名片图拿来当封面。
	if payload.CoverURL != "https://cdn.example/cover.jpg" {
		t.Fatalf("应用公众号封面，实际 %q", payload.CoverURL)
	}
}

// FoodTalks 署名会把「编译/翻译/采访」并进来，外发按原文逐项拆出来写进文末声明。
func TestBylineSplitsTranslatorCredits(t *testing.T) {
	credits, ok := parseByline("作者：Chloe Sorvino，翻译：Lemin。")
	if !ok || strings.Join(credits, "|") != "作者：Chloe Sorvino|翻译：Lemin" {
		t.Fatalf("credits=%v ok=%v", credits, ok)
	}
	credits, ok = parseByline("英文来源：Whole Foods Market")
	if !ok || strings.Join(credits, "|") != "英文来源：Whole Foods Market" {
		t.Fatalf("credits=%v ok=%v", credits, ok)
	}
	if _, ok := parseByline("作者是谁，这很重要。"); ok {
		t.Fatal("正文句不能当署名拆")
	}
	if _, ok := parseByline("来源：艾媒咨询《2025新式茶饮报告》"); ok {
		t.Fatal("数据出处「来源：」不是署名")
	}
}

// 公众号里左右滑着看的几张图，外发平台上竖着排，图注里的滑动提示去掉（#349 知乎号兼职手发时删了）。
func TestDistributionCaptionDropsSwipeHint(t *testing.T) {
	cases := map[string][]string{
		"百奇的包装变化；图片来源：格力高": {"百奇的包装变化（向左滑动查看更多）", "图片来源：格力高"},
		"新品一览；图片来源：品牌":     {"新品一览，左滑查看更多", "图片来源：品牌"},
		"图片来源：品牌":          {"(左右滑动查看)", "图片来源：品牌"},
		"滑雪场一角；图片来源：雪场":    {"滑雪场一角", "图片来源：雪场"},
	}
	for want, texts := range cases {
		var blocks []Block
		for _, text := range texts {
			blocks = append(blocks, Block{K: "cap", Text: text})
		}
		if got := mergedCaption(blocks); got != want {
			t.Errorf("%q 并成 %q，要 %q", texts, got, want)
		}
	}
}
