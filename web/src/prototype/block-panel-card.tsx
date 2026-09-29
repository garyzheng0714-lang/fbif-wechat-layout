import { useState } from "react"
import { Crop, GripVertical, ImageUp, MoreHorizontal, Trash2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Segmented } from "@/components/ui/segmented"
import { Surface } from "@/components/ui/surface"
import { Switch } from "@/components/ui/switch"
import { Tooltip } from "@/components/ui/tooltip"

/**
 * 底部区域卡：内容照现有排版工具的 block-panel（本文显示、规则、各区块开关、更多文章），只用 Su Design 的组件摆。
 * - 标题行：「底部区域」+ 本文显示开关；关掉后下面的设置都用不上，一起收起（不再单独放折叠按钮）。
 * - 更多文章平放在卡里，用折痕线分开，不在白卡里再套卡片；每篇的换图、裁剪、删除收进行末的「更多」菜单。
 * 正式实现接 mountBlockPanel 的状态与 more-articles 的抓取 / 替换图片 / 裁剪 / Sortable 拖动排序。
 */

const BLOCKS = [
  { id: "signature", label: "参考文献前署名图" },
  { id: "tip-title", label: "提示标题" },
  { id: "tip-body", label: "提示正文" },
  { id: "card", label: "Bobo 名片" },
  { id: "more", label: "更多文章" },
] as const

const MORE_ARTICLES = [
  {
    id: "a",
    url: "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjc5Mg==",
    title: "没想到！英国人把豆腐做到英国第一，还像卖衣服一样卖出XXL超大号！",
    image: "/prototype-media/image3.jpg",
  },
  {
    id: "b",
    url: "https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjc5Mg==",
    title: "全球茶叶品牌观察：从一片叶子到一门生意",
    image: "/prototype-media/image8.jpg",
  },
]

export function BlockPanelCard() {
  const [shown, setShown] = useState(true)
  const [rule, setRule] = useState("auto")
  const [enabled, setEnabled] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(BLOCKS.map((b) => [b.id, true]))
  )

  return (
    <Surface>
      <div className="flex items-center gap-2">
        <label htmlFor="block-shown" className="flex-1 text-sm font-medium text-fg">
          底部区域
        </label>
        <Switch id="block-shown" checked={shown} onCheckedChange={setShown} />
      </div>

      {shown ? (
        <>
          <Field>
            <FieldLabel asChild>
              <span>规则</span>
            </FieldLabel>
            <Segmented
              aria-label="底部区域规则"
              group="block-rule"
              className="w-full *:flex-1"
              value={rule}
              onValueChange={setRule}
              items={[
                { value: "auto", label: "自动" },
                { value: "default", label: "默认" },
                { value: "mote", label: "Mote 作者默认" },
              ]}
            />
          </Field>

          <div className="grid gap-(--ds-gap-row)">
            {BLOCKS.map((b) => (
              <div key={b.id} className="flex h-(--ds-h-row) items-center gap-2">
                <label htmlFor={`block-${b.id}`} className="flex-1 text-sm text-fg">
                  {b.label}
                </label>
                <Switch
                  id={`block-${b.id}`}
                  checked={enabled[b.id]}
                  onCheckedChange={(v) => setEnabled((s) => ({ ...s, [b.id]: v }))}
                />
              </div>
            ))}
          </div>

          {enabled.more ? <MoreArticles /> : null}
        </>
      ) : null}
    </Surface>
  )
}

function MoreArticles() {
  return (
    <div className="grid gap-item">
      <ol aria-label="更多文章" className="grid divide-y divide-line">
        {MORE_ARTICLES.map((a, i) => (
          <li key={a.id} className="grid gap-part py-item first:pt-0">
            <div className="flex items-center gap-2">
              <GripVertical className="size-4 shrink-0 cursor-grab text-fg-subtle" aria-hidden />
              <Input defaultValue={a.url} aria-label={`第 ${i + 1} 篇链接`} className="flex-1" />
              <ArticleMenu index={i + 1} />
            </div>
            {/* 抓到的卡片：封面按公众号「更多文章」卡片的 2.35:1 缩小显示，左缘和上面的链接框对齐 */}
            <div className="flex gap-2 pl-6">
              <img src={a.image} alt="" className="aspect-[2.35/1] w-28 shrink-0 rounded-sm object-cover" />
              <p className="line-clamp-2 min-w-0 flex-1 text-sm text-fg">{a.title}</p>
            </div>
          </li>
        ))}
      </ol>
      <div>
        <Button variant="ghost" size="sm" className="edge-start">
          清空更多文章
        </Button>
      </div>
    </div>
  )
}

function ArticleMenu({ index }: { index: number }) {
  return (
    <DropdownMenu>
      <Tooltip content="更多操作" side="left">
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="edge-end" aria-label={`第 ${index} 篇的更多操作`}>
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
      </Tooltip>
      <DropdownMenuContent align="end">
        <DropdownMenuItem>
          <ImageUp />
          替换图片
        </DropdownMenuItem>
        <DropdownMenuItem>
          <Crop />
          调整裁剪
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem>
          <Trash2 />
          删除
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
