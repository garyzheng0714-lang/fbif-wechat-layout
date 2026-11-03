package main

import (
	"strings"
	"testing"
)

// 2026-11-03 用户：「为什么我们的原创文章你标注成了转载？然后我们的转载你标注为原创？」
// #338 文末写着「本文为FBIF原创」，参考来源里一条「经济日报（人民政协网转载）」让卡片判成转载、没外发；
// #340 是读者 EZ 的投稿，公众号没声明原创、文末也没写，卡片判成原创、自动发到了头条号和百家号。
const (
	provenance338Blocks = `[{"k":"txt","runs":[{"type":"txt","text":"作者：Mote莫特"}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"这是我茶叶系列的第41篇，也是茶叶系列的终篇。"}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"参考来源："}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"[18] 农业生产装上“数字大脑”，2026年3月18日，经济日报（人民政协网转载）"}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"提示：","bold":true}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"* 本文为FBIF原创，欢迎转发朋友圈；"}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"* 转载请在后台回复“转载”了解规则；"}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）"}]}]`
	provenance340Blocks = `[{"k":"txt","runs":[{"type":"txt","text":"作者：EZ"}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"在中国，如果一款豆腐卖不好，可能是因为名字起得不好或者包装设计不够诱人。"}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"提示：","bold":true}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"* 转载请在后台回复“转载”了解规则；"}]},` +
		`{"k":"txt","runs":[{"type":"txt","text":"* FBIF投稿与商务合作，联系Bobo（微信：fbifmedia1）"}]}]`
)

func TestOwnAccountArticleIsOriginalOnlyWhenDeclared(t *testing.T) {
	for _, c := range []struct {
		name   string
		job    publishJob
		label  string
		review bool
	}{
		{"#338 文末声明原创，参考来源里带「转载」", publishJob{SourceName: "FBIF食品饮料创新", AISource: "FBIF食品饮料创新", BlocksJSON: provenance338Blocks}, "原创", true},
		{"#340 投稿，没声明原创", publishJob{SourceName: "FBIF食品饮料创新", AISource: "FBIF食品饮料创新", BlocksJSON: provenance340Blocks}, "转载", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if original, _, evidence := publishArticleProvenance(c.job); original != c.review {
				t.Errorf("判定 original=%v（%s），应为 %v", original, evidence, c.review)
			}
			published := c.job
			published.Stage, published.PublicationAction, published.FoodTalksNewsID = publishStagePublished, publishPublicationActionManualCMS, 1
			if err := externalDistributionEligibility(published); (err == nil) != c.review {
				t.Errorf("外发资格 err=%v，应为能外发=%v", err, c.review)
			}
			if meta := reviewBatchMeta(c.job); !strings.Contains(meta, ">"+c.label+"<") {
				t.Errorf("审核卡应标「%s」：%s", c.label, meta)
			}
		})
	}
}

// 原创要有明说：公众号标了原创，或正文一行「本文为<自家>原创」；明写转载、别家出处、什么都没说的都是转载。
func TestArticleProvenanceNeedsExplicitOriginalDeclaration(t *testing.T) {
	t.Setenv(siteOwnAccountsEnv, "")
	line := func(texts ...string) string {
		var b strings.Builder
		b.WriteString("[")
		for i, text := range texts {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"k":"txt","runs":[{"type":"txt","text":"` + text + `"}]}`)
		}
		b.WriteString("]")
		return b.String()
	}
	const own = "FBIF食品饮料创新"
	for _, c := range []struct {
		name     string
		job      publishJob
		original bool
		source   string
	}{
		{"自家号、公众号标了原创、文末没写", publishJob{SourceName: own, SourceDeclaredOriginal: true, BlocksJSON: line("正文")}, true, own},
		{"自家号、文末简写「本文为FBIF原创」", publishJob{SourceName: own, BlocksJSON: line("正文", "* 本文为FBIF原创，欢迎转发朋友圈；")}, true, own},
		{"自家号、什么都没声明（投稿）", publishJob{SourceName: own, BlocksJSON: line("作者：EZ", "正文")}, false, own},
		{"自家号标了原创、正文又明写转载", publishJob{SourceName: own, SourceDeclaredOriginal: true, AISource: "联商网", BlocksJSON: line("正文", "* 本文为转载文章。")}, false, "联商网"},
		{"自家号、AI 抽到别家出处", publishJob{SourceName: own, AISource: "小食代", BlocksJSON: line("正文")}, false, "小食代"},
		{"自家号、文末「本文为新零售原创」", publishJob{SourceName: own, BlocksJSON: line("正文", "本文为新零售原创，作者：张三")}, false, "新零售"},
		{"别家公众号标了原创", publishJob{SourceName: "小食代", SourceDeclaredOriginal: true, BlocksJSON: line("正文")}, false, "小食代"},
		{"自家号用公众号「转载」功能转的、正文只写转载（#337）", publishJob{SourceName: own, SourceRepostedFrom: "轻食online", BlocksJSON: line("正文", "* 本文为转载文章。")}, false, "轻食online"},
		{"公众号「转载」记的原号压过 AI 读出的出处", publishJob{SourceName: own, SourceRepostedFrom: "轻食online", AISource: "ONICHA官网", BlocksJSON: line("正文")}, false, "轻食online"},
		{"正文里引文带「转载」二字不算声明", publishJob{SourceName: own, SourceDeclaredOriginal: true, BlocksJSON: line("[3] 某报道，经济日报（人民政协网转载）", "转载请在后台回复“转载”了解规则")}, true, own},
	} {
		t.Run(c.name, func(t *testing.T) {
			original, source, evidence := publishArticleProvenance(c.job)
			if original != c.original || source != c.source {
				t.Fatalf("original=%v source=%q（%s），应为 original=%v source=%q", original, source, evidence, c.original, c.source)
			}
			if evidence == "" {
				t.Fatal("要说凭什么这样判")
			}
		})
	}
}

func TestWechatDeclaredOriginalReadsCopyrightStat(t *testing.T) {
	for raw, want := range map[string]bool{
		`<script>var _copyright_stat = "1";</script>`: true,
		`<script>var _copyright_stat="1";</script>`:   true,
		`<script>var _copyright_stat = "2";</script>`: false,
		`<script>var _copyright_stat = "0";</script>`: false,
		`<script>var ct = "1";</script>`:              false,
	} {
		if got := wechatDeclaredOriginal(raw); got != want {
			t.Errorf("%s → %v，应为 %v", raw, got, want)
		}
	}
	job := publishJob{SourceDeclaredOriginal: true}
	applyWechatSourceMeta(&job, publishFetchedArticle{})
	if job.SourceDeclaredOriginal {
		t.Fatal("重新抓到的原文没标原创，要照新抓的记")
	}
	applyWechatSourceMeta(&job, publishFetchedArticle{DeclaredOriginal: true})
	if !job.SourceDeclaredOriginal {
		t.Fatal("抓到公众号标了原创要记下")
	}
}

// #337 页面里摘下来的几处：公众号「转载」功能转来的文章，页面数据和开头卡片都记着原号。
const wechatRepostPage = `<div class="original_area_primary"><p role="option" class="original_primary_card_tips">
<span>以下文章来源于轻食online</span><span>，作者hanyuting</span></p>
<a class="weui-flex original_primary_card" href="#" id="copyright_info"><div class="weui-flex__bd">
<strong class="original_primary_nickname">轻食online</strong></div></a></div>
<script>window.cgiData = { author: 'hanyuting',
  copyright_info: {
    copyright_stat: '2' * 1,
    platform: '轻食online',
    reprint_url: 'http://mp.weixin.qq.com/s?__biz=Mzk2NDM4OTQ1NA%3D%3D\x26amp;mid=2247503223\x26amp;idx=1\x26amp;sn=86950d343e0859f03537768183e79478',
    source_username: 'gh_7960b6c488a0',
  },
};</script>
<script>var _copyright_stat = "2"; var nickname = htmlDecode("FBIF食品饮料创新"); var msg_source_url = '';</script>`

func TestWechatRepostSourceReadsRepostCard(t *testing.T) {
	from, url := wechatRepostSource(wechatRepostPage)
	if from != "轻食online" {
		t.Fatalf("原号 = %q，应为轻食online", from)
	}
	if want := "http://mp.weixin.qq.com/s?__biz=Mzk2NDM4OTQ1NA%3D%3D&mid=2247503223&idx=1&sn=86950d343e0859f03537768183e79478"; url != want {
		t.Fatalf("原文 = %q，应为 %q", url, want)
	}
	// 页面数据缺了，开头卡片上的号名也认。
	cardOnly := strings.Replace(wechatRepostPage, "platform: '轻食online',", "", 1)
	if from, _ := wechatRepostSource(cardOnly); from != "轻食online" {
		t.Fatalf("只有卡片时原号 = %q", from)
	}
	// 不是转载（原创、没声明）的，卡片样式类名、旧数据都不算。
	for _, stat := range []string{"1", "0"} {
		page := strings.Replace(wechatRepostPage, `var _copyright_stat = "2"`, `var _copyright_stat = "`+stat+`"`, 1)
		if from, url := wechatRepostSource(page); from != "" || url != "" {
			t.Fatalf("_copyright_stat=%s 不是转载，却读出 %q %q", stat, from, url)
		}
	}
	job := publishJob{SourceRepostedFrom: "旧号"}
	applyWechatSourceMeta(&job, publishFetchedArticle{})
	if job.SourceRepostedFrom != "" {
		t.Fatal("重新抓到的原文不是转载，要照新抓的记")
	}
	if p := articleBlocksProvenance("FBIF食品饮料创新", []Block{{K: "txt", Text: "正文"}}, false, "轻食online"); p == nil || p.Original || p.Source != "轻食online" {
		t.Fatalf("网页导入的判定 = %+v，应为转载、来源轻食online", p)
	}
}
