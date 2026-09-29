import * as React from "react"
import { motion, useTransform } from "motion/react"
import { Switch as SwitchPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { rubberband, useEdges } from "@/components/ui/stretch"

/**
 * 开关：立即生效的设置用它；需要保存才生效的用 Checkbox。轨道 30×18，内边距 2，圆钮 14。
 * 动法照 iOS 的开关，全部由弹簧算出、可以打断：
 * - 按下：圆钮朝要去的方向伸长 4（手指压扁它）；没切换就松手，弹回圆形。
 * - 切换：圆钮左右两条边各走一条弹簧，前沿先到、后沿跟上，途中被拉长，到位后缩回圆形；轨道的墨色由圆钮位置算出，和位移同拍、同样可打断。
 * - 拖动：按住圆钮左右拖，它跟着手走，拖过两端有橡皮筋；松手时按圆钮停在哪一半决定开关，从当前位置弹过去。
 * 减少动态效果时圆钮与底色一起瞬间到位。
 */
const KNOB = 14
const TRAVEL = 12 // 轨道内宽 26 − 圆钮 14
const PRESS = 4

function useControllable(value: boolean | undefined, initial: boolean | undefined, onChange?: (v: boolean) => void) {
  const [inner, setInner] = React.useState(initial ?? false)
  const on = value ?? inner
  const set = (v: boolean) => {
    if (value === undefined) setInner(v)
    onChange?.(v)
  }
  return [on, set] as const
}

function Switch({
  className,
  checked,
  defaultChecked,
  onCheckedChange,
  disabled,
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  const [on, setOn] = useControllable(checked, defaultChecked, onCheckedChange)
  const rest = (v: boolean) => ({ left: v ? TRAVEL : 0, right: (v ? TRAVEL : 0) + KNOB })
  const { left, width, moveTo } = useEdges(rest(on))
  // 墨色层的不透明度 = 圆钮走过的比例（拖动、打断时也跟着位置走）
  const ink = useTransform(left, [0, TRAVEL], [0, 1], { clamp: true })
  const first = React.useRef(true)
  const drag = React.useRef<{ x: number; start: number; moved: boolean } | null>(null)
  const dragged = React.useRef(false)

  React.useEffect(() => {
    moveTo(rest(on), { instant: first.current })
    first.current = false
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [on])

  const press = (e: React.PointerEvent<HTMLButtonElement>) => {
    if (disabled || e.button !== 0) return
    e.currentTarget.setPointerCapture(e.pointerId)
    drag.current = { x: e.clientX, start: left.get(), moved: false }
    const r = rest(on)
    moveTo(on ? { left: r.left - PRESS, right: r.right } : { left: r.left, right: r.right + PRESS })
  }
  const move = (e: React.PointerEvent<HTMLButtonElement>) => {
    const d = drag.current
    if (!d) return
    // 外层可能被缩放（缩略图、展示台）：按元素的实际缩放把指针位移换算回布局像素
    const scale = e.currentTarget.getBoundingClientRect().width / e.currentTarget.offsetWidth || 1
    const dx = (e.clientX - d.x) / scale
    if (!d.moved && Math.abs(dx) < 3) return
    d.moved = true
    const x = rubberband(d.start + dx, 0, TRAVEL, TRAVEL)
    moveTo({ left: x, right: x + KNOB + PRESS / 2 }, { instant: true })
  }
  const release = () => {
    const d = drag.current
    drag.current = null
    if (!d) return
    if (d.moved) {
      const next = left.get() + (KNOB + PRESS / 2) / 2 > (TRAVEL + KNOB) / 2
      if (next !== on) setOn(next)
      else moveTo(rest(on))
    } else moveTo(rest(on))
  }

  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      checked={on}
      onCheckedChange={setOn}
      disabled={disabled}
      onPointerDown={press}
      onPointerMove={move}
      onPointerUp={release}
      onPointerCancel={release}
      // 拖动过的这一下不算点击，开关已经在松手时按位置决定了
      onClick={(e) => {
        if (dragged.current) {
          e.preventDefault()
          dragged.current = false
        }
      }}
      onPointerUpCapture={() => {
        dragged.current = Boolean(drag.current?.moved)
      }}
      className={cn(
        "group relative inline-flex h-4.5 w-7.5 shrink-0 cursor-pointer touch-none items-center rounded-full p-(--ds-pad-track) outline-none",
        "bg-well shadow-inset transition-[background-color] duration-(--ds-dur-fast) ease-ds hover:bg-active",
        "focus-visible:ring-2 focus-visible:ring-focus/40 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas",
        "disabled:cursor-not-allowed disabled:opacity-40",
        className
      )}
      {...props}
    >
      <motion.span aria-hidden className="absolute inset-0 rounded-full bg-primary" style={{ opacity: ink }} />
      <SwitchPrimitive.Thumb asChild>
        <motion.span
          data-slot="switch-thumb"
          className="absolute top-(--ds-pad-track) left-(--ds-pad-track) block h-3.5 rounded-full bg-card shadow-raised"
          style={{ x: left, width }}
        />
      </SwitchPrimitive.Thumb>
    </SwitchPrimitive.Root>
  )
}


export { Switch }
