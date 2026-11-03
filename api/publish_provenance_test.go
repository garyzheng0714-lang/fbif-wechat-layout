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
			if original, _ := publishArticleProvenance(c.job); original != c.review {
				t.Errorf("网站与排版的判定 original=%v，应为 %v", original, c.review)
			}
			if status, _, evidence := publishExternalDistributionProvenance(c.job); (status == publishExternalProvenanceOriginal) != c.review {
				t.Errorf("外发判定 %s（%s），应为原创=%v", status, evidence, c.review)
			}
			if meta := reviewBatchMeta(c.job); !strings.Contains(meta, ">"+c.label+"<") {
				t.Errorf("审核卡应标「%s」：%s", c.label, meta)
			}
		})
	}
}
