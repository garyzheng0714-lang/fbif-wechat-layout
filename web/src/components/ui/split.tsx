import * as React from "react"
import { Slot } from "radix-ui"

import { cn } from "@/lib/utils"

/**
 * 分栏（DESIGN.md §3.1 Split）：2–3 栏并排，占满父级高度。固定栏定宽，主栏 flex-1，宽度不按百分比伸缩（L&S《网格被高估》）。
 * 每栏各自滚动，滚到头不带动外层（overscroll-behavior: contain）。
 * 栏与栏之间的分界三选一，都不叠加：
 * - gap（默认）：整体铺侧栏底色，内容栏各是一张白卡（卡片阴影，圆角 = 外框 16 − 缝 8，同心），栏与栏之间是 8 的缝。不画线，靠「面」分开。
 *   导航这类贴边的栏用 surface="none"，直接落在底色上。
 * - none：相邻两栏底色不同（侧栏 bg-sidebar 挨着白页）时，底色交界就是分界。
 * - line：1px 分隔线，只给放在卡片里的小尺寸示例。隐藏的栏（hidden）不算，第一条可见的栏左边不画线。
 * 不管换页和开合动画：栏要进出时，由使用方给那一栏加 starting: 过渡（只动 transform 与透明度）。
 */

/** 宽度阶梯（DESIGN.md §3.2）：栏只能取这些值，其余空间给 flex-1 的主栏。 */
type PaneWidth = 56 | 228 | 240 | 256 | 300 | 368 | 400 | 460 | 560

const WIDTH: Record<PaneWidth, string> = {
  56: "w-14",
  228: "w-57",
  240: "w-60",
  256: "w-64",
  300: "w-75",
  368: "w-92",
  400: "w-100",
  460: "w-115",
  560: "w-140",
}

type Divider = "gap" | "none" | "line"
const SplitContext = React.createContext<Divider>("none")

function Split({ divider = "gap", className, ...props }: React.ComponentProps<"div"> & { divider?: Divider }) {
  return (
    <SplitContext value={divider}>
      <div
        data-slot="split"
        data-divider={divider}
        className={cn(
          "flex h-full min-h-0 w-full min-w-0",
          divider === "line" && "[&>[data-slot=split-pane]:not([hidden])~[data-slot=split-pane]]:border-l [&>*]:border-line",
          // 缝：右、上、下各 8；左边由第一栏决定——贴边的导航栏直接从边上开始，卡片栏自己再让出 8
          divider === "gap" && "gap-(--ds-gutter) bg-sidebar py-(--ds-gutter) pr-(--ds-gutter) [&>[data-surface=card]:first-child]:ml-(--ds-gutter)",
          className
        )}
        {...props}
      />
    </SplitContext>
  )
}

/**
 * 一栏：传 width 即定宽（取自宽度阶梯），不传即主栏 flex-1。
 * scroll={false} 时这一栏自己不滚，由里面的区域滚（例如顶栏固定、下面列表滚）。asChild 把样式交给 nav、aside 等语义元素。
 */
function SplitPane({
  width,
  scroll = true,
  asChild = false,
  surface = "card",
  className,
  ...props
}: React.ComponentProps<"div"> & {
  width?: PaneWidth
  scroll?: boolean
  asChild?: boolean
  /** 仅 divider="gap"：card 是一张白卡；none 直接落在底色上（导航、图标栏）。卡片会设 --ds-surface，吸顶的分组标题用它当底色 */
  surface?: "card" | "none"
}) {
  const Comp = asChild ? Slot.Root : "div"
  const card = React.useContext(SplitContext) === "gap" && surface === "card"
  return (
    <Comp
      data-slot="split-pane"
      data-surface={card ? "card" : undefined}
      className={cn(
        "flex min-h-0 min-w-0 flex-col",
        // 同心：卡片圆角 = 外框圆角 − 缝
        card && "rounded-[calc(var(--ds-r-card)-var(--ds-gutter))] bg-card shadow-card [--ds-surface:var(--ds-card)]",
        width ? cn(WIDTH[width], "shrink-0") : "flex-1",
        scroll ? "overflow-y-auto overscroll-contain" : "overflow-hidden",
        className
      )}
      {...props}
    />
  )
}

export { Split, SplitPane }
export type { PaneWidth }
