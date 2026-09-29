import { useEffect, useRef } from "react"
import { animate, useMotionValue, useMotionValueEvent, useReducedMotion, type MotionValue } from "motion/react"

import { DUR, EASE_OUT, SPRINGS } from "@/components/ui/ease"

/**
 * 前后沿弹簧：一个会移动的块（开关圆钮、分段滑块、标签指示条）左右两条边各走一条弹簧。
 * 往哪边走，哪边就是前沿，用快一点的弹簧先到；另一边是后沿，慢一点跟上——移动途中块被自然拉长，到位后缩回原宽。
 * 弹簧由 motion 按时间算出（不是 CSS 曲线）：中途换目标时从当前位置、当前速度接着走，不会跳。
 * 减少动态效果时直接到位。
 */
export function useEdges(initial: { left: number; right: number }) {
  const left = useMotionValue(initial.left)
  const right = useMotionValue(initial.right)
  // 宽度是自己的 MotionValue，由两条边的变化推出来；不用 useTransform(() => …)：
  // 它在 StrictMode 下或挂载同一帧里直接 set 时会漏掉这次更新，宽度停在旧值（曾停在 0）
  const width = useMotionValue(initial.right - initial.left)
  const sync = () => width.set(right.get() - left.get())
  useEffect(() => {
    sync()
    const offs = [left.on("change", sync), right.on("change", sync)]
    return () => offs.forEach((off) => off())
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const reduce = useReducedMotion()
  const running = useRef<{ stop: () => void }[]>([])

  /** 移到新位置；instant 用于首帧和键盘高频操作 */
  const moveTo = (to: { left: number; right: number }, opts: { instant?: boolean } = {}) => {
    running.current.forEach((a) => a.stop())
    if (opts.instant || reduce) {
      left.set(to.left)
      right.set(to.right)
      sync()
      running.current = []
      return
    }
    const forward = to.left + to.right >= left.get() + right.get()
    running.current = [
      animate(left, to.left, forward ? SPRINGS.trail : SPRINGS.lead),
      animate(right, to.right, forward ? SPRINGS.lead : SPRINGS.trail),
    ]
  }

  useEffect(() => () => running.current.forEach((a) => a.stop()), [])
  return { left, right, width, moveTo }
}

/**
 * 橡皮筋：超出 [min, max] 的部分按苹果滚动视图的公式衰减，拖得越远越难拉，松手弹回。
 * dimension 是可拖动范围的尺寸，c = 0.55 取自 UIScrollView 的阻尼系数。
 */
export function rubberband(value: number, min: number, max: number, dimension: number, c = 0.55) {
  const over = (d: number) => (1 - 1 / ((d * c) / dimension + 1)) * dimension
  if (value < min) return min - over(min - value)
  if (value > max) return max + over(value - max)
  return value
}

/**
 * 按下缩放（按钮 .97、复选框与单选 .9）：按下 100ms ease-out 立刻压下（手指按下时不能等），松手 snappy 回弹，中途反悔从当前速度接着走。
 * 只响应主键指针，键盘不动；减少动态效果时直接到位。把 node 挂到元素的 ref 上，press / release 挂到指针事件上
 *（release 同时挂 pointerup、pointerleave、pointercancel）。直接写 style.transform，不触发 React 渲染。
 */
export function usePressScale<T extends HTMLElement>(to: number) {
  const node = useRef<T | null>(null)
  const scale = useMotionValue(1)
  const reduce = useReducedMotion()
  useMotionValueEvent(scale, "change", (v) => {
    if (node.current) node.current.style.transform = v === 1 ? "" : `scale(${v})`
  })
  const press = (e: { button: number }) => {
    if (e.button !== 0) return
    if (reduce) scale.set(to)
    else animate(scale, to, { duration: DUR.press, ease: EASE_OUT })
  }
  const release = () => {
    if (scale.get() === 1 && !scale.isAnimating()) return
    if (reduce) scale.set(1)
    else animate(scale, 1, SPRINGS.snappy)
  }
  return { node, press, release }
}

export type { MotionValue }
