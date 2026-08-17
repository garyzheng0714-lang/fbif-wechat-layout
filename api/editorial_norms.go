package main

// editorial_norms.go — M7「编辑部规范倒推提示词」。AI 预填的 system prompt 不再是
// 代码里手写的字符串，而是从两份数据资产组装：
//
//	api/editorial/norms_prompt.md — 声明式规范 + 真实 few-shot（唯一真相源是
//	  docs/editorial-norms-foodtalks.md，规范变更先改文档再同步这里）
//	api/editorial/tag_vocab.json  — 近 12 个月编辑部实际用过的高频标签词表
//	  （含 tag_id，供标签查询接口核验 AI 标签是否命中真实标签库）
//
// 规范与模型解耦：prompt 只有声明式规则和范例，无模型特定语法；换模型只改
// ARK_MODEL_ID 并重跑 publish_enrich_eval_test.go 评测（达标线见规范文档 §7）。

import (
	_ "embed"
	"encoding/json"
	"log"
	"strings"
	"sync"
)

//go:embed editorial/norms_prompt.md
var editorialNormsPromptTemplate string

//go:embed editorial/tag_vocab.json
var editorialTagVocabRaw []byte

// editorialVocabPromptTopN 注入 prompt 的词表条数：词表按频次降序，前 300 覆盖
// 绝大多数常用标签；再长徒增 token，长尾靠「主角品牌可直接用」规则兜住。
const editorialVocabPromptTopN = 300

type editorialTag struct {
	TagID   int64  `json:"tag_id"`
	TagName string `json:"tag_name"`
	Cnt     int    `json:"cnt"`
}

var (
	editorialOnce      sync.Once
	editorialTags      []editorialTag
	editorialPromptStr string
)

// editorialInit 解析词表并把 {{VOCAB}} 占位替换成前 N 个标签名（顿号分隔）。
// 词表是 go:embed 编译期资产，解析失败属构建级错误：留空词表照常出 prompt，
// 但打 log 显式暴露（规则十二：不静默）。
func editorialInit() {
	if err := json.Unmarshal(editorialTagVocabRaw, &editorialTags); err != nil {
		log.Printf("editorial: WARN 标签词表解析失败（prompt 将不含词表）: %v", err)
		editorialTags = nil
	}
	names := make([]string, 0, editorialVocabPromptTopN)
	for _, t := range editorialTags {
		name := strings.TrimSpace(t.TagName)
		// 栏目/活动标签（FBIF2026会后报告、Wow食品创新奖…）由编辑手工体系管理，
		// AI 不打（编辑规范 §4）——词表历史混入的这类词不再注入 prompt 供 AI 选用。
		if name == "" || publishSiteTagBlocked(name) {
			continue
		}
		names = append(names, name)
		if len(names) >= editorialVocabPromptTopN {
			break
		}
	}
	editorialPromptStr = strings.ReplaceAll(editorialNormsPromptTemplate, "{{VOCAB}}", strings.Join(names, "、"))
}

// editorialEnrichSystemPrompt AI 预填的 system prompt（组装一次全程复用）。
func editorialEnrichSystemPrompt() string {
	editorialOnce.Do(editorialInit)
	return editorialPromptStr
}

// editorialTagVocab 完整词表（含 tag_id），供标签查询核验与测试断言用。
func editorialTagVocab() []editorialTag {
	editorialOnce.Do(editorialInit)
	return editorialTags
}
