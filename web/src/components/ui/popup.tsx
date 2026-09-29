"use client"

import * as React from "react"
import { motion, useReducedMotion, type Transition } from "motion/react"

import { cn } from "@/lib/utils"

import { DUR, EASE_OUT, SPRINGS } from "@/components/ui/ease"
import { fromKeyboard } from "@/components/ui/hotkeys"

/**
 * 弹出层共用的进出场（菜单、弹出层、右键菜单、对话框、命令面板）。
 * Radix 的 Presence 只认 CSS keyframes，所以内容层一律 forceMount，交给 motion 的 AnimatePresence 管进出；两套机制不叠在同一个元素上。
 * - 打开：从 from（缩放 / 位移）回到原位走弹簧，透明度 200ms ease-out 同时开始；
 * - 关闭：原路退回，比打开快一档——位移、缩放走 SPRINGS.snappy（从当前速度接着走，中途反悔不跳），透明度 150ms ease-out；退场期间不再接收指针；
 * - 键盘打开或关闭（回车、方向键、Esc、⌘K）：0ms，直接出现、直接消失（DESIGN.md §5.1）；
 * - 减少动态效果：不缩放、不位移，只保留淡入淡出。
 */


/** 根组件的开关状态：受控、非受控两种写法照旧；同时放进 context，内容层据此挂载或播退场。 */
const PopupContext = React.createContext<{ open: boolean; setOpen: (open: boolean) => void }>({ open: false, setOpen: () => {} })

function useOpenState(open: boolean | undefined, defaultOpen: boolean | undefined, onOpenChange?: (open: boolean) => void) {
  const [inner, setInner] = React.useState(defaultOpen ?? false)
  const value = open ?? inner
  const setOpen = React.useCallback(
    (next: boolean) => {
      if (open === undefined) setInner(next)
      onOpenChange?.(next)
    },
    [open, onOpenChange]
  )
  return { open: value, setOpen }
}

const usePopup = () => React.useContext(PopupContext)

type Offset = { x?: number; y?: number; scale?: number }

/** 进出场的外壳：替代 Radix 内容层本身（asChild），from 是出现前的样子。AnimatePresence 的 custom 传「这次是不是键盘关的」。 */
function PopupMotion({
  from,
  spring = SPRINGS.snappy,
  ...props
}: React.ComponentProps<typeof motion.div> & { from: Offset; spring?: Transition }) {
  const reduce = useReducedMotion()
  const [instant] = React.useState(fromKeyboard)
  const rest: Offset = Object.fromEntries(Object.keys(from).map((k) => [k, k === "scale" ? 1 : 0]))
  const variants = {
    hidden: (viaKeyboard?: boolean) => ({
      opacity: 0,
      ...(viaKeyboard || reduce ? rest : from),
      pointerEvents: "none" as const,
      transition: viaKeyboard ? { duration: 0 } : { ...SPRINGS.snappy, opacity: { duration: DUR.fast, ease: EASE_OUT } },
    }),
    shown: { opacity: 1, ...rest, pointerEvents: "auto" as const, transition: { ...spring, opacity: { duration: DUR.base, ease: EASE_OUT } } },
  }
  return <motion.div initial={instant ? false : "hidden"} animate="shown" exit="hidden" variants={variants} {...props} />
}

/** 遮罩：只淡入淡出，和面板同时开始；键盘开关时 0ms。 */
function PopupScrim({ className, ...props }: React.ComponentProps<typeof motion.div>) {
  const [instant] = React.useState(fromKeyboard)
  const variants = {
    hidden: (viaKeyboard?: boolean) => ({ opacity: 0, transition: viaKeyboard ? { duration: 0 } : { duration: DUR.fast, ease: EASE_OUT } }),
    shown: { opacity: 1, transition: { duration: DUR.base, ease: EASE_OUT } },
  }
  return (
    <motion.div
      initial={instant ? false : "hidden"}
      animate="shown"
      exit="hidden"
      variants={variants}
      className={cn("fixed inset-0 z-50 bg-scrim", className)}
      {...props}
    />
  )
}

export { PopupContext, PopupMotion, PopupScrim, useOpenState, usePopup }
