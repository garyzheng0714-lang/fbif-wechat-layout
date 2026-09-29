import { BadgeCheck, Heart, MessageSquare, Share, ThumbsUp } from "lucide-react"

import { EmptyState } from "@/components/ui/empty-state"

import "./article-preview.css"
import sampleHtml from "./sample-article.html?raw"

import type { Account } from "./accounts"

const TITLE = "累计销量突破3.5亿罐、零售额20亿，小罐茶做对了什么，又做错了什么？"

/**
 * 预览区：结构照现有排版工具——标题、账号署名行、正文，底部是微信式底栏（红线 6）。
 * 文章直接在白底上流动（无 phone-frame，红线 11）。原型里的正文是用现有 fbif 渲染器从真实飞书导出排出来的
 * HTML（本机样例，不入库）；正式实现由 engine.js 写入 #contentArea，React 不管理其中的节点。
 */
export function ArticlePreview({ account }: { account: Account }) {
  // 整张预览卡一起滚（顶栏吸顶、正文从栏下没入），微信式底栏吸底
  return (
    <>
      <article className="article-preview">
        <h1 className="article-preview-title">{TITLE}</h1>
        <p className="article-preview-meta">
          <b>{account.name}</b>
          <span>2026年9月29日</span>
          <span>上海</span>
        </p>
        <div dangerouslySetInnerHTML={{ __html: sampleHtml }} />
      </article>
      <footer className="wx-bar">
        <div className="wx-bar-inner">
          <img src={account.avatar} alt="" className="wx-bar-avatar" />
          <span className="wx-bar-name">{account.name}</span>
          <BadgeCheck className="wx-bar-verified" aria-hidden />
          <span className="wx-bar-stats">
            <span><ThumbsUp aria-hidden />59</span>
            <span><Share aria-hidden />117</span>
            <span><Heart aria-hidden />17</span>
            <span><MessageSquare aria-hidden />13</span>
          </span>
        </div>
      </footer>
    </>
  )
}

/** 空状态只放一句字（DESIGN.md §4.0-8）：怎么导入由左栏的输入框占位和按钮说清楚，这里不再放第二个「选择文件」。 */
export function PreviewEmpty() {
  return (
    <div className="grid flex-1 place-items-center pb-16">
      <EmptyState title="还没有内容" />
    </div>
  )
}
