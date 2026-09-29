import * as React from "react"

import { cn } from "@/lib/utils"

/**
 * 面：凡是「装东西的」都用它——一张卡、一块凹槽、一扇窗、一个展示台。它不决定里面放什么，只决定里面的间距。
 *
 * 间距由它自己的宽度算出来（ds-tokens.css「间距由容器算」），不手填：
 * - 边 → 内容 `p-inset` = 8 + 宽 ÷ 24；组与组 `gap-group`、同组的项 `gap-item`、一项里的部件 `gap-part` 依次按比例收紧。
 * - 同一张卡放进窄栏自动变紧、放上大舞台自动变松；里面再嵌的容器又按它自己的宽度重算。
 * - 面里再套的面（窗口里的一条消息、卡里的一块凹槽）：比例部分减半（8 + 宽 ÷ 48），圆角自动同心（外圆角 − 外内边距，rounded-in）。
 * - 贴着内边距的其他东西（卡里的一张图）也用 `rounded-in`。
 * 宽度由外面给（网格、栏、max-w）：它是尺寸容器，不会被内容撑宽。
 *
 * tone：card 白纸带影（正在处理的东西）· well 凹槽 · line 发丝线框（展示台）· none 只给间距、不画面。
 * 只有 card、well 算「纸」（套在里面的面才减半）；展示台和 none 只是摆东西的地方，放进去的卡片仍按顶层算。
 * bar：窗口的标题栏 / 手机的状态栏：贴着上边、44 高，是边框的一部分，不吃上内边距；左右和内容同一条线（px-inset）。
 */
const TONE = {
  card: "bg-card text-fg shadow-card",
  well: "bg-well",
  line: "shadow-[inset_0_0_0_1px_var(--ds-line)]",
  none: "",
} as const

const Nested = React.createContext(false)

function Surface({
  tone = "card",
  bar,
  className,
  bodyClassName,
  children,
  ...props
}: React.ComponentProps<"div"> & { tone?: keyof typeof TONE; bar?: React.ReactNode; bodyClassName?: string }) {
  const nested = React.useContext(Nested)
  const paper = tone === "card" || tone === "well"
  return (
    <div
      data-slot="surface"
      data-space
      data-surface={paper || undefined}
      data-tone={tone}
      className={cn(tone !== "none" && (nested ? "rounded-in" : "rounded-card"), TONE[tone], bar != null && "overflow-hidden", className)}
      {...props}
    >
      {bar != null ? (
        <div data-slot="surface-bar" className="flex h-11 shrink-0 items-center gap-2 px-inset">
          {bar}
        </div>
      ) : null}
      <div data-slot="surface-body" className={cn("grid gap-group p-inset", bodyClassName)}>
        <Nested value={nested || paper}>{children}</Nested>
      </div>
    </div>
  )
}

export { Surface }
