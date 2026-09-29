import * as React from "react"
import { animate, motion, useMotionValue, useReducedMotion, useTransform } from "motion/react"
import { Progress as ProgressPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { SPRINGS } from "@/components/ui/ease"

/**
 * 进度条：已知进度的长任务。时长未知时用 Spinner，不要伪造进度。
 * 轨道高 4、圆角全圆，与滑块轨道一致。
 *
 * 动效（docs/研究/动效-苹果.md §5）：
 * - 值变化 → 进度条整条平移（translateX，不用 scaleX：缩放会把圆头压扁）→ smooth 弹簧（0.35s / bounce 0）。
 * - 弹簧是物理参数：上一段还没走完又来新值（上传进度连续上报）时，从当前位置、当前速度接着走，不会一顿一顿。
 * - 首帧直接在位；减少动态效果时每次都直接到位。
 */
function Progress({
  className,
  value,
  max = 100,
  ...props
}: React.ComponentProps<typeof ProgressPrimitive.Root>) {
  const percent = Math.min(100, Math.max(0, ((value ?? 0) / max) * 100))
  const reduce = useReducedMotion()
  const shown = useMotionValue(percent)
  const x = useTransform(shown, (p) => `${p - 100}%`)

  React.useEffect(() => {
    if (reduce) shown.jump(percent)
    else if (shown.get() !== percent) animate(shown, percent, SPRINGS.smooth)
  }, [percent, reduce, shown])

  return (
    <ProgressPrimitive.Root
      data-slot="progress"
      value={value}
      max={max}
      className={cn("relative h-1 w-full overflow-hidden rounded-full bg-well shadow-inset", className)}
      {...props}
    >
      <ProgressPrimitive.Indicator asChild>
        <motion.div data-slot="progress-indicator" className="size-full rounded-full bg-primary" style={{ x }} />
      </ProgressPrimitive.Indicator>
    </ProgressPrimitive.Root>
  )
}

export { Progress }
