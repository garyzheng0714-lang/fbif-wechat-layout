import * as React from "react"
import { AnimatePresence, motion, useReducedMotion, type HTMLMotionProps } from "motion/react"

import { cn } from "@/lib/utils"
import { DUR, EASE_OUT, SPRINGS } from "@/components/ui/ease"
import { LiftedCard } from "@/components/ui/lifted-card"
import { fromKeyboard } from "@/components/ui/hotkeys"

/**
 * 列表行，两种排法：
 * - stacked（默认）：标题（至多两行）+ 一行元信息。内边距 12，标题 14，元信息 12。
 * - inline：一行 38 高——未读点 · 来源（宽 112）· 标题（截断）· 时间。未读时来源和标题加粗。
 * 选中时灰底直接换到这一行（LiftedCard fill，不滑动）。
 *
 * 动效（只在 ListGroup 里）：插入、删除、重排 → 行的位置走 layout，smooth 弹簧（0.35s / bounce 0）；
 * 新行 opacity 0 → 1、scale .98 → 1（smooth）；删掉的行原地 opacity → 0、scale → .98，150ms，同时让出位置，下面的行 smooth 补上。
 * 行高不变。键盘触发（例如按 Delete 删行）直接到位；减少动态效果时位移直接到位，只保留淡入淡出。
 */
const ListGroupContext = React.createContext(false)

const INSTANT = { duration: 0 } as const
const ENTER = { ...SPRINGS.smooth, layout: SPRINGS.smooth } as const
/** 减少动态效果：位置、缩放直接到位，透明度照常淡入 */
const GENTLE = { duration: DUR.fast, ease: EASE_OUT, layout: INSTANT, scale: INSTANT } as const
const EXIT = { opacity: 0, scale: 0.98, transition: { duration: DUR.fast, ease: EASE_OUT } } as const

/**
 * 会增删、重排的一组 ListItem：子项要带稳定的 key。删掉的行弹出文档流（popLayout），其余行同时补位。
 * 位置以这一组为参照（layoutRoot）：整组被外面的布局挤动时行跟着走，只有组内换位才动。
 * 默认 grid、行间距 2。
 */
function ListGroup({ className, children, ...props }: React.ComponentProps<"div">) {
  return (
    <motion.div layout layoutRoot data-slot="list-group" className={cn("relative grid gap-0.5", className)} {...(props as HTMLMotionProps<"div">)}>
      <ListGroupContext.Provider value>
        <AnimatePresence initial={false} mode="popLayout">
          {React.Children.toArray(children)}
        </AnimatePresence>
      </ListGroupContext.Provider>
    </motion.div>
  )
}

/** 在 ListGroup 里换成 motion.button，其余情况是普通 button，不挂任何动画。 */
function Row(props: React.ComponentProps<"button">) {
  const grouped = React.useContext(ListGroupContext)
  const reduce = useReducedMotion()
  if (!grouped) return <button type="button" {...props} />
  const keyboard = fromKeyboard()
  return (
    <motion.button
      type="button"
      layout="position"
      initial={{ opacity: 0, scale: 0.98 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={keyboard ? { opacity: 0, transition: INSTANT } : EXIT}
      transition={keyboard ? INSTANT : reduce ? GENTLE : ENTER}
      {...(props as HTMLMotionProps<"button">)}
    />
  )
}
function ListItem({
  className,
  title,
  meta,
  trailing,
  selected = false,
  unread = false,
  layout = "stacked",
  group = "list",
  ...props
}: Omit<React.ComponentProps<"button">, "title"> & {
  title: React.ReactNode
  meta?: React.ReactNode
  trailing?: React.ReactNode
  selected?: boolean
  /** 只在 inline 排法里有效：左侧蓝点 + 加粗 */
  unread?: boolean
  layout?: "stacked" | "inline"
  group?: string
}) {
  // 未读不加粗：加粗会让同一行的时间和来源标记移动 1.5–3px（layout-shift 审计实测），未读靠蓝点与深色文字区分
  const base = cn(
    "relative w-full rounded-row text-left outline-none",
    "not-aria-pressed:hover:bg-hover",
    "focus-visible:ring-2 focus-visible:ring-focus/40",
    "disabled:pointer-events-none disabled:opacity-40"
  )
  if (layout === "inline") {
    return (
      <Row data-slot="list-item" aria-pressed={selected} className={cn(base, "flex h-9.5 items-center pr-3 pl-2", className)} {...props}>
        {selected ? <LiftedCard group={group} className="rounded-row" /> : null}
        <span className={cn("relative flex w-full min-w-0 items-center gap-3 text-sm", unread || selected ? "text-fg" : "text-fg-muted")}>
          <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", unread ? "bg-accent" : "bg-transparent")} />
          {unread ? <span className="sr-only">未读</span> : null}
          <span className="flex w-28 shrink-0 items-center gap-1.5 truncate">{meta}</span>
          <span className="min-w-0 flex-1 truncate">{title}</span>
          {trailing ? <span className="shrink-0 text-xs text-fg-subtle tabular-nums">{trailing}</span> : null}
        </span>
      </Row>
    )
  }
  return (
    <Row data-slot="list-item" aria-pressed={selected} className={cn(base, "grid p-3", className)} {...props}>
      {selected ? <LiftedCard group={group} className="rounded-row" /> : null}
      <span className="relative grid gap-1">
        <span className="line-clamp-2 text-sm text-fg">{title}</span>
        {meta || trailing ? (
          <span className="flex min-w-0 items-center gap-2 text-xs text-fg-subtle">
            <span className="flex min-w-0 flex-1 items-center gap-2">{meta}</span>
            {trailing ? <span className="shrink-0 tabular-nums">{trailing}</span> : null}
          </span>
        ) : null}
      </span>
    </Row>
  )
}

/** 分组名（按日期或类别），滚动时吸顶，底色与画布一致。与上一组相隔 24。 */
function ListSection({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn("sticky top-0 z-20 bg-(--ds-surface,var(--ds-canvas)) px-3 pt-6 pb-2 text-xs font-medium text-fg-subtle", className)}
      {...props}
    />
  )
}

export { ListGroup, ListItem, ListSection }
