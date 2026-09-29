"use client"

import * as React from "react"
import {
  animate,
  AnimatePresence,
  motion,
  useMotionValue,
  usePresence,
  useReducedMotion,
  type AnimationPlaybackControls,
} from "motion/react"
import { Tooltip as TooltipPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { Keys } from "@/components/ui/kbd"

import { DUR, EASE_OUT, SPRINGS } from "@/components/ui/ease"
import { fromKeyboard } from "@/components/ui/hotkeys"

/** 整个应用只放一个：第一个提示等 400ms，之后 300ms 内移到相邻按钮的提示直接出现、不做动画（成组）。 */
function TooltipProvider(props: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return <TooltipPrimitive.Provider delayDuration={400} skipDelayDuration={300} {...props} />
}

/** 正在退场的提示：相邻的提示直接出现时，让它们立刻消失，不和新提示叠在一起。 */
const leaving = new Set<() => void>()

/**
 * 提示的外壳（Radix 内容层 asChild 到这里，进出场交给 motion，不叠 Radix 的 CSS 动画）。
 * - 悬停等过延迟后出现：以触发边为原点（Radix 给的 transform-origin）scale .97→1 走 SPRINGS.snappy，透明度 150ms ease-out；
 * - 同组第二个起（Radix 标成 instant-open）、键盘聚焦：0ms 直接出现，还在退场的上一个立刻消失；
 * - 离开：透明度 100ms，缩回 .97；键盘移走焦点时 0ms；减少动态效果只淡入淡出。
 */
function TooltipBubble({ className, ref, style, ...props }: React.ComponentProps<typeof motion.div>) {
  const [present, safeToRemove] = usePresence()
  const reduce = useReducedMotion()
  const node = React.useRef<HTMLDivElement>(null)
  const opacity = useMotionValue(0)
  const scale = useMotionValue(1)
  const running = React.useRef<AnimationPlaybackControls[]>([])

  React.useLayoutEffect(() => {
    running.current.forEach((a) => a.stop())
    running.current = []
    if (present) {
      leaving.forEach((cut) => cut())
      if (node.current?.dataset.state === "instant-open" || fromKeyboard()) {
        opacity.set(1)
        scale.set(1)
        return
      }
      if (opacity.get() === 0) scale.jump(reduce ? 1 : 0.97)
      running.current = [animate(opacity, 1, { duration: DUR.fast, ease: EASE_OUT }), animate(scale, 1, SPRINGS.snappy)]
      return
    }
    if (fromKeyboard()) {
      opacity.set(0)
      safeToRemove()
      return
    }
    const cut = () => {
      leaving.delete(cut)
      running.current.forEach((a) => a.stop())
      opacity.set(0)
      safeToRemove()
    }
    leaving.add(cut)
    const fade = animate(opacity, 0, { duration: DUR.press, ease: EASE_OUT })
    running.current = reduce ? [fade] : [fade, animate(scale, 0.97, { duration: DUR.press, ease: EASE_OUT })]
    fade.then(() => leaving.has(cut) && cut())
    return () => void leaving.delete(cut)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [present])

  // Radix 通过 asChild 传进来的 ref（定位要用）和自己读 data-state 的 ref 合在一起
  const attach = (el: HTMLDivElement | null) => {
    node.current = el
    if (typeof ref === "function") ref(el)
    else if (ref) ref.current = el
  }

  return (
    <motion.div
      ref={attach}
      data-slot="tooltip-content"
      className={cn(
        "z-50 flex origin-(--radix-tooltip-content-transform-origin) items-center gap-2 rounded-control bg-inverse px-2 py-1 text-xs font-medium text-inverse-fg select-none",
        className
      )}
      {...props}
      style={{ ...style, opacity, scale }}
    />
  )
}

/**
 * 文字提示：图标按钮必须配 Tooltip，写清动作和快捷键。
 * 第一次延迟 400ms 后从触发元素长出来；在一组按钮间移动时直接出现（DESIGN.md §5.2）。动效见 TooltipBubble。
 */
function Tooltip({
  content,
  shortcut,
  side = "bottom",
  children,
}: {
  content: React.ReactNode
  shortcut?: string
  side?: "top" | "bottom" | "left" | "right"
  children: React.ReactElement
}) {
  const [open, setOpen] = React.useState(false)
  return (
    <TooltipPrimitive.Root open={open} onOpenChange={setOpen}>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <AnimatePresence>
        {open ? (
          <TooltipPrimitive.Portal forceMount>
            {/* 读屏用的隐藏副本默认会把 children 再渲染一遍（连同动效外壳）；文字提示直接给它文字 */}
            <TooltipPrimitive.Content
              forceMount
              asChild
              side={side}
              sideOffset={8}
              collisionPadding={8}
              aria-label={typeof content === "string" ? [content, shortcut].filter(Boolean).join(" ") : undefined}
            >
              <TooltipBubble>
                {content}
                {shortcut ? <Keys keys={shortcut} className="text-inverse-fg/50" /> : null}
              </TooltipBubble>
            </TooltipPrimitive.Content>
          </TooltipPrimitive.Portal>
        ) : null}
      </AnimatePresence>
    </TooltipPrimitive.Root>
  )
}

export { Tooltip, TooltipProvider }
