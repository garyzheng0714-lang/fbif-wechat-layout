import * as React from "react"
import { animate, motion, useMotionValue, useReducedMotion } from "motion/react"

import { cn } from "@/lib/utils"
import { SPRINGS } from "@/components/ui/ease"
import { fromKeyboard } from "@/components/ui/hotkeys"
import { rubberband, useEdges } from "@/components/ui/stretch"

/**
 * 当前项的标记。
 * - fill（默认）：灰底，放在当前项内部、内容之下，直接出现、不滑动。用在导航、列表、分页。
 * - raised：白底凸起的滑块，只用在凹槽轨道里（分段切换、标签页）。放在轨道里（轨道要 relative），整条轨道只有这一块，
 *   它自己找到轨道里的当前项（data-state=on / active、aria-selected、aria-checked）并盖在它下面。
 *
 * raised 的动效（docs/研究/动效-苹果.md §5；全部是物理弹簧，可以打断，中途反向从当前位置、当前速度接着走）：
 * - 指针换项 → 左右两条边各走一条弹簧：前沿 SPRINGS.lead（snappy 0.22 / 0.15）先到，后沿 SPRINGS.trail（smooth 0.35 / 0）跟上，
 *   途中块被拉长约 40%，到位缩回（stretch.ts 的 useEdges）。
 * - 指针按住当前项 → 滑块 scale .96（SPRINGS.snappy），松手回 1；标签页按下即切换，滑块在新项上同样缩着滑过去。
 * - 拖动（传 onPick 时）→ 按住当前项左右拖，滑块中心跟手（SPRINGS.follow），宽度在相邻两项之间插值；拖过两端有橡皮筋，靠墙那边贴住轨道、块被挤窄；
 *   松手按滑块中心吸到最近一项（onPick），从当前位置、带着速度弹过去。
 * - 键盘换项 → 直接到位，0ms（DESIGN.md §5.1）；减少动态效果时位移与缩放都瞬间到位。
 * - 尺寸变化（字体加载、窗口变宽）→ 直接贴到新位置，不动画。
 */


const CURRENT = ":scope > [data-state=on], :scope > [data-state=active], :scope > [aria-selected=true], :scope > [aria-checked=true]"
const ITEMS = ":scope > :not([data-slot=lift]):not([data-disabled]):not(:disabled)"
const PRESS = 0.96

type Rect = { left: number; right: number; top: number; height: number }
const rectOf = (el: HTMLElement): Rect => ({
  left: el.offsetLeft,
  right: el.offsetLeft + el.offsetWidth,
  top: el.offsetTop,
  height: el.offsetHeight,
})

export function LiftedCard({
  group,
  variant = "fill",
  className,
  onPick,
}: {
  group: string
  variant?: "fill" | "raised"
  className?: string
  /** 仅 raised：传了就能拖动滑块，松手时回传吸到的那一项 */
  onPick?: (item: HTMLElement) => void
}) {
  if (variant === "fill") {
    return <span aria-hidden data-slot="lift" className={cn("absolute inset-0 z-0 bg-active", className)} />
  }
  return <RaisedLift group={group} className={className} onPick={onPick} />
}

function RaisedLift({ group, className, onPick }: { group: string; className?: string; onPick?: (item: HTMLElement) => void }) {
  const ref = React.useRef<HTMLSpanElement>(null)
  const { left, right, moveTo } = useEdges({ left: 0, right: 0 })
  // 宽度自己同步，不用 useEdges 的 width：那是 useTransform，StrictMode 重挂载时会丢掉挂载那一刻的更新，首帧宽度停在 0
  const width = useMotionValue(0)
  const top = useMotionValue(0)
  const height = useMotionValue(0)
  const scale = useMotionValue(1)
  const reduce = useReducedMotion()
  // 回调与减少动态在渲染之间会变，监听只挂一次，从 ref 里读最新的
  const latest = React.useRef({ onPick, reduce })
  React.useLayoutEffect(() => {
    latest.current = { onPick, reduce }
  })

  React.useLayoutEffect(() => {
    const lift = ref.current
    const track = lift?.parentElement
    if (!lift || !track) return
    const doc = track.ownerDocument
    let target: Rect | null = null
    let pressed: Element | null = null
    let drag: { x: number; center: number; zoom: number; moved: boolean } | null = null
    let swallowClick = false

    const current = () => track.querySelector<HTMLElement>(CURRENT)
    // 减少动态效果时不缩（缩放属于「弹性」，苹果在这个设置下一并关掉）
    const squeeze = (to: number) => {
      if (latest.current.reduce) scale.set(1)
      else animate(scale, to, SPRINGS.snappy)
    }

    /** 贴到当前项。animated：由换项引起（指针点了别的项）；否则是首帧或尺寸变化，直接贴上。 */
    const place = (animated: boolean) => {
      const item = current()
      lift.style.visibility = item ? "" : "hidden"
      if (!item) return (target = null)
      const r = rectOf(item)
      if (target && r.left === target.left && r.right === target.right && r.top === target.top && r.height === target.height) return
      // 竖排的标签页换行时不做横向拉伸，直接到位
      const instant = !animated || !target || r.top !== target.top || fromKeyboard()
      target = r
      top.set(r.top)
      height.set(r.height)
      if (!drag?.moved) moveTo(r, { instant })
      // 标签页按下即切换：按着的那一项成了当前项，滑块照样缩着
      if (pressed && item.contains(pressed)) squeeze(PRESS)
    }

    const down = (e: PointerEvent) => {
      if (e.button !== 0) return
      const item = current()
      pressed = e.target as Element
      if (!item || !item.contains(pressed)) return
      squeeze(PRESS)
      if (latest.current.onPick) {
        const zoom = track.getBoundingClientRect().width / track.offsetWidth || 1
        drag = { x: e.clientX, center: (left.get() + right.get()) / 2, zoom, moved: false }
      }
    }

    /**
     * 滑块中心在 c 时的左右边：宽度在相邻两项之间按中心的位置插值。
     * 拖过两端时中心按橡皮筋收住（尺度取一项的宽，最多多走小半项），靠墙那条边贴住轨道内沿不出界，块被挤窄。
     */
    const edgesAt = (c: number) => {
      const items = [...track.querySelectorAll<HTMLElement>(ITEMS)].map(rectOf)
      const first = items[0]
      const last = items.at(-1)!
      const mid = items.map((r) => (r.left + r.right) / 2)
      const x = rubberband(c, mid[0], mid.at(-1)!, first.right - first.left)
      let i = 0
      while (i < mid.length - 2 && x > mid[i + 1]) i++
      const t = mid.length > 1 ? Math.min(1, Math.max(0, (x - mid[i]) / (mid[i + 1] - mid[i]))) : 0
      const next = items[Math.min(i + 1, items.length - 1)]
      const w = (items[i].right - items[i].left) * (1 - t) + (next.right - next.left) * t
      return { left: Math.max(first.left, x - w / 2), right: Math.min(last.right, x + w / 2) }
    }

    const move = (e: PointerEvent) => {
      if (!drag) return
      const dx = (e.clientX - drag.x) / drag.zoom
      if (!drag.moved && Math.abs(dx) < 3) return
      drag.moved = true
      const to = edgesAt(drag.center + dx)
      if (latest.current.reduce) moveTo(to, { instant: true })
      else {
        animate(left, to.left, SPRINGS.follow)
        animate(right, to.right, SPRINGS.follow)
      }
    }

    const up = () => {
      pressed = null
      if (scale.get() !== 1 || scale.isAnimating()) squeeze(1)
      const d = drag
      drag = null
      if (!d?.moved) return
      // 这一下是拖动，不算点击；松手时已经按位置决定了去哪一项
      swallowClick = true
      setTimeout(() => (swallowClick = false))
      const center = (left.get() + right.get()) / 2
      const items = [...track.querySelectorAll<HTMLElement>(ITEMS)]
      const nearest = items.reduce<HTMLElement | null>((best, el) => {
        const r = rectOf(el)
        const b = best && rectOf(best)
        return !b || Math.abs((r.left + r.right) / 2 - center) < Math.abs((b.left + b.right) / 2 - center) ? el : best
      }, null)
      if (nearest && nearest !== current()) latest.current.onPick?.(nearest)
      else if (target) moveTo(target)
    }
    const click = (e: MouseEvent) => {
      if (!swallowClick) return
      e.preventDefault()
      e.stopPropagation()
    }

    const sync = () => width.set(right.get() - left.get())
    const offs = [left.on("change", sync), right.on("change", sync)]
    place(false)
    sync()
    const mo = new MutationObserver(() => place(true))
    mo.observe(track, { subtree: true, childList: true, attributes: true, attributeFilter: ["data-state", "aria-selected", "aria-checked"] })
    const ro = new ResizeObserver(() => place(false))
    ro.observe(track)
    for (const child of track.children) ro.observe(child)
    track.addEventListener("pointerdown", down)
    track.addEventListener("click", click, true)
    doc.addEventListener("pointermove", move)
    doc.addEventListener("pointerup", up)
    doc.addEventListener("pointercancel", up)
    return () => {
      offs.forEach((off) => off())
      mo.disconnect()
      ro.disconnect()
      track.removeEventListener("pointerdown", down)
      track.removeEventListener("click", click, true)
      doc.removeEventListener("pointermove", move)
      doc.removeEventListener("pointerup", up)
      doc.removeEventListener("pointercancel", up)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <motion.span
      ref={ref}
      aria-hidden
      data-slot="lift"
      data-group={group}
      className={cn("pointer-events-none absolute top-0 left-0 z-0 bg-card shadow-raised", className)}
      style={{ x: left, width, top, height, scale }}
    />
  )
}
