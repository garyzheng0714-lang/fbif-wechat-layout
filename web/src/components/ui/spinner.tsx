import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

/**
 * 加载中：时长未知的短等待，颜色跟随 currentColor。超过几秒的任务用 Progress 或骨架屏。
 * 尺寸 12 / 16 / 20，分别配 12 / 14 / 16 的文字；线宽 1.5，与图标一致。
 *
 * 动效（docs/研究/动效-苹果.md §5）：
 * - 常驻 → 整圈匀速旋转，0.8 秒一圈（linear：它是状态，不是动作，不要缓动）。
 * - delay（毫秒）→ 区块级加载传 400：400ms 内完成的操作不闪出加载圈；到点后透明度 150ms 淡入（延迟期间占位但不可见）。按钮内用默认 0，立刻出现。
 * - 减少动态效果 → 不转，改成 2 秒一次的慢速透明度呼吸（它本身是状态，不能完全静止）。
 */
const KEYFRAMES = "@keyframes ds-spin{to{transform:rotate(1turn)}}@keyframes ds-breathe{50%{opacity:.35}}"

const spinnerVariants = cva(
  "shrink-0 animate-[ds-spin_0.8s_linear_infinite] motion-reduce:animate-[ds-breathe_2s_cubic-bezier(0.4,0,0.6,1)_infinite]",
  {
    variants: {
      size: { sm: "size-3", md: "size-4", lg: "size-5" },
    },
    defaultVariants: { size: "md" },
  }
)

function Spinner({
  className,
  size,
  label = "加载中",
  delay = 0,
  ...props
}: React.ComponentProps<"span"> & VariantProps<typeof spinnerVariants> & { label?: string; delay?: number }) {
  const [shown, setShown] = React.useState(delay <= 0)
  React.useEffect(() => {
    if (delay <= 0) return
    const t = window.setTimeout(() => setShown(true), delay)
    return () => window.clearTimeout(t)
  }, [delay])
  return (
    <span
      role="status"
      data-slot="spinner"
      data-state={shown ? "visible" : "waiting"}
      className={cn("inline-flex transition-opacity duration-(--ds-dur-fast) ease-ds", !shown && "invisible opacity-0", className)}
      {...props}
    >
      <style href="ds-spinner" precedence="default">
        {KEYFRAMES}
      </style>
      <svg viewBox="0 0 16 16" fill="none" aria-hidden className={spinnerVariants({ size })}>
        <circle cx="8" cy="8" r="6.25" stroke="currentColor" strokeOpacity="0.2" strokeWidth="1.5" />
        <path d="M14.25 8A6.25 6.25 0 0 0 8 1.75" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
      </svg>
      <span className="sr-only">{label}</span>
    </span>
  )
}

export { Spinner, spinnerVariants }
