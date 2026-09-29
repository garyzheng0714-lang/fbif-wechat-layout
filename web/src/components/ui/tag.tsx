import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { AnimatePresence, motion, useReducedMotion, type HTMLMotionProps } from "motion/react"

import { cn } from "@/lib/utils"
import { DUR, EASE_OUT, SPRINGS } from "@/components/ui/ease"
import { fromKeyboard } from "@/components/ui/hotkeys"

/**
 * 标签：不用彩色底。分类为灰色文字；编号为细描边等宽字；状态为「圆点 + 灰色文字」，颜色只在圆点上；
 * chip 是灰底小标签（“Beta”“Updates”那种），用在标题旁的标记和文章标签。高 20，字号 12。
 *
 * 动效（只在 TagGroup 里）：加入 → scale .9 → 1、opacity 0 → 1，snappy 弹簧（0.22s / bounce 0.15）；
 * 后面的标签 layout 让出宽度（snappy）；移除 → scale → .9、opacity → 0，150ms，同时弹出文档流，后面的标签 snappy 补位。
 * 位置以这一组为参照，整组被挤动时不动。键盘触发直接到位；减少动态效果时不缩放、不位移，只淡入淡出。
 */
const TagGroupContext = React.createContext(false)

const INSTANT = { duration: 0 } as const
// 透明度晚 50ms：先让后面的标签挪开一点，新标签再显出来，不和邻居叠在一起
const ENTER = { ...SPRINGS.snappy, opacity: { duration: DUR.fast, ease: EASE_OUT, delay: 0.05 }, layout: SPRINGS.snappy } as const
const GENTLE = { duration: DUR.fast, ease: EASE_OUT, scale: INSTANT, layout: INSTANT } as const
const EXIT = { opacity: 0, scale: 0.9, transition: { duration: DUR.fast, ease: EASE_OUT } } as const

/** 会增删的一组标签：子项带稳定 key。默认横排、可换行、间距 12。 */
function TagGroup({ className, children, ...props }: React.ComponentProps<"div">) {
  return (
    <motion.div
      layout
      layoutRoot
      data-slot="tag-group"
      className={cn("relative flex flex-wrap items-center gap-3", className)}
      {...(props as HTMLMotionProps<"div">)}
    >
      <TagGroupContext.Provider value>
        <AnimatePresence initial={false} mode="popLayout">
          {React.Children.toArray(children)}
        </AnimatePresence>
      </TagGroupContext.Provider>
    </motion.div>
  )
}
const tagVariants = cva("inline-flex h-5 shrink-0 items-center gap-1 text-xs whitespace-nowrap", {
  variants: {
    variant: {
      text: "text-fg-subtle",
      code: "rounded-sm px-1 font-mono text-fg-subtle shadow-[inset_0_0_0_1px_var(--ds-line-strong)]",
      chip: "rounded-sm bg-active px-1.5 text-fg-muted",
      neutral: "text-fg-muted",
      success: "text-fg-muted",
      warning: "text-fg-muted",
      danger: "text-fg-muted",
    },
  },
  defaultVariants: { variant: "text" },
})

const DOT = {
  neutral: "bg-fg-subtle",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
} as const

function Tag({
  className,
  variant,
  children,
  ...props
}: React.ComponentProps<"span"> & VariantProps<typeof tagVariants>) {
  const grouped = React.useContext(TagGroupContext)
  const reduce = useReducedMotion()
  const dot = variant && variant in DOT ? DOT[variant as keyof typeof DOT] : null
  const inner = (
    <>
      {dot ? <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", dot)} /> : null}
      {children}
    </>
  )
  if (!grouped) {
    return (
      <span data-slot="tag" className={cn(tagVariants({ variant }), className)} {...props}>
        {inner}
      </span>
    )
  }
  const keyboard = fromKeyboard()
  return (
    <motion.span
      data-slot="tag"
      layout="position"
      initial={{ opacity: 0, scale: 0.9 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={keyboard ? { opacity: 0, transition: INSTANT } : reduce ? { opacity: 0 } : EXIT}
      transition={keyboard ? INSTANT : reduce ? GENTLE : ENTER}
      className={cn(tagVariants({ variant }), className)}
      {...(props as HTMLMotionProps<"span">)}
    >
      {inner}
    </motion.span>
  )
}

export { Tag, TagGroup, tagVariants }
