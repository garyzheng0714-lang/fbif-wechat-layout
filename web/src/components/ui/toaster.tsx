"use client"

import * as React from "react"
import {
  AnimatePresence,
  animate,
  motion,
  useMotionValue,
  usePresence,
  useReducedMotion,
  useTransform,
  type MotionValue,
} from "motion/react"
import { CircleAlert, CircleCheck, Info, TriangleAlert } from "lucide-react"
import { toast as sonner, useSonner, type Action, type ToasterProps, type ToastT } from "sonner"

import { cn } from "@/lib/utils"
import { DUR, EASE_OUT, SPRINGS } from "@/components/ui/ease"
import { Spinner } from "@/components/ui/spinner"
import { rubberband } from "@/components/ui/stretch"

/**
 * 轻提示：操作完成后的一句话反馈，底部居中，可带一个撤销类操作。调用照旧用 sonner 的 toast()，这里只负责画和动。
 * 最小高 40（左右 16），宽 356，圆角 10，bg-card + shadow-popover；操作按钮离边 4、圆角 6（同心）。
 *
 * 动效（docs/研究/动效-苹果.md §5，堆叠与滑走的阈值照 Sonner）：
 * - 出现 → 从自己一个身位之下升起、透明度 0 → 1 → bouncy 弹簧（0.40s / bounce 0.25，过冲 2.8%）：像灵动岛那样轻轻落定。
 * - 堆叠 → 新的一条在最前；后面的每往后一层上移 12、缩小 5%、高度收成和最前一条一样、文字淡出（只露一条边）→ smooth 弹簧（0.35s / bounce 0）。
 *   指针移上去（或键盘焦点进来）展开成一列、间距 12，移开收回；同样是 smooth，中途来回从当前位置、当前速度接着走。
 * - 滑走 → 按住横向或向下拖，1:1 跟手；向上拖按橡皮筋越拉越紧。松手时拖过 45px 或平均速度超过 0.11px/ms 就带着松手速度飞出去（smooth）并 150ms 淡出；
 *   不够就带着速度弹回原位（bouncy）。
 * - 离开（到时、撤销、toast.dismiss）→ 下沉一个身位并淡出 → 150ms ease-out，比出现快一档；后面那条 smooth 补上来。
 * - 等待结果（toast.promise）→ 外框高度 smooth 过渡，图标和文字在 blur 4px 里交叉替换（150ms）。
 * - 进度线：底边 1px，在停留时长内匀速收短；指针在提示上、按住、页面切到后台时和计时一起暂停。
 * - 减少动态效果：升起、堆叠、滑走都瞬间到位，只保留透明度淡入淡出；不画进度线。
 * 只支持底部居中（position 等 sonner 的布局参数不生效）；同屏最多 3 条（visibleToasts）。
 */
type Id = ToastT["id"]
const PEEK = 12 // 收起时每往后一层露出的边
const GAP = 12 // 展开时两条之间的间距
const SHRINK = 0.05 // 每往后一层缩小的比例
const SWIPE_DISTANCE = 45
const SWIPE_VELOCITY = 0.11 // px/ms，松手时整段拖动的平均速度
const KEYFRAMES = "@keyframes ds-toast-progress{from{transform:scaleX(1)}to{transform:scaleX(0)}}"

type Registry = Map<Id, { extent: () => number; visible: boolean }>

function Toaster({ duration = 4000, visibleToasts = 3, expand = false, icons, className, style }: ToasterProps) {
  const { toasts } = useSonner()
  const [heights, setHeights] = React.useState<Record<string, number>>({})
  const [hovered, setHovered] = React.useState(false)
  const [interacting, setInteracting] = React.useState(false)
  const hidden = useDocumentHidden()
  const expanded = expand || hovered
  // 列表的高度 = 这一叠提示实际占到的高度（由每条的弹簧算出）：它就是悬停热区，展开后条与条之间的空隙也算在里面
  const extent = useMotionValue(0)
  const registry = React.useRef<Registry>(new Map())
  const measure = React.useCallback(() => {
    let top = 0
    registry.current.forEach((item) => item.visible && (top = Math.max(top, item.extent())))
    extent.set(top)
  }, [extent])
  // h < 0：这条已经移走，忘掉它的高度
  const setHeight = React.useCallback(
    (id: Id, h: number) =>
      setHeights((all) => {
        if (all[id] === h || (h < 0 && !(id in all))) return all
        const next = { ...all, [id]: h }
        if (h < 0) delete next[id]
        return next
      }),
    []
  )

  React.useEffect(() => {
    if (!toasts.length) setHovered(false)
  }, [toasts.length])

  const front = toasts[0] ? (heights[toasts[0].id] ?? 0) : 0
  let before = 0
  return (
    <section aria-label="通知" aria-live="polite" aria-relevant="additions text" aria-atomic="false">
      <style href="ds-toast-progress" precedence="default">
        {KEYFRAMES}
      </style>
      <motion.ol
        data-slot="toaster"
        data-expanded={expanded}
        tabIndex={-1}
        className={cn("fixed bottom-6 left-1/2 z-100 w-89 max-w-[calc(100vw-32px)] -translate-x-1/2 outline-none", className)}
        style={{ ...style, height: extent }}
        onMouseEnter={() => setHovered(true)}
        onMouseMove={() => setHovered(true)}
        onMouseLeave={() => !interacting && setHovered(false)}
        onFocus={() => setHovered(true)}
        onBlur={(e) => !e.currentTarget.contains(e.relatedTarget) && setHovered(false)}
        onPointerDown={() => setInteracting(true)}
        onPointerUp={() => setInteracting(false)}
        onPointerCancel={() => setInteracting(false)}
      >
        <AnimatePresence>
          {toasts.map((t, i) => {
            const offset = before
            before += (heights[t.id] ?? 0) + GAP
            return (
              <ToastItem
                key={t.id}
                toast={t}
                index={i}
                count={toasts.length}
                visible={i < visibleToasts}
                expanded={expanded}
                paused={expanded || interacting || hidden}
                duration={t.duration ?? duration}
                offset={offset}
                front={front}
                icons={icons}
                registry={registry.current}
                measure={measure}
                onHeight={setHeight}
              />
            )
          })}
        </AnimatePresence>
      </motion.ol>
    </section>
  )
}

type ItemProps = {
  toast: ToastT
  index: number
  count: number
  visible: boolean
  expanded: boolean
  paused: boolean
  duration: number
  /** 展开时前面各条的高度 + 间距之和 */
  offset: number
  /** 最前一条的高度：收起时后面各条都收成这么高 */
  front: number
  icons: ToasterProps["icons"]
  registry: Registry
  measure: () => void
  onHeight: (id: Id, h: number) => void
}

function ToastItem({ toast: t, index, count, visible, expanded, paused, duration, offset, front, icons, registry, measure, onHeight }: ItemProps) {
  const reduce = useReducedMotion()
  const [isPresent, safeToRemove] = usePresence()
  const content = React.useRef<HTMLDivElement>(null)
  const [natural, setNatural] = React.useState(0)
  const y = useMotionValue(0) // 在一叠里的位置（向上为负）
  const scale = useMotionValue(1)
  const opacity = useMotionValue(0)
  const height = useMotionValue<number | "auto">("auto")
  const swipeX = useMotionValue(0)
  const swipeY = useMotionValue(0)
  const shownY = useTransform(() => y.get() + swipeY.get())
  const entered = React.useRef(false)
  // 滑走时飞出去的那段淡出；有它就不再播普通的离开
  const flying = React.useRef<Promise<unknown> | null>(null)
  const autoClosed = React.useRef(false)

  // 自然高度：内容变了（等待 → 成功换了文案）就重新量，外框跟着弹过去
  React.useLayoutEffect(() => {
    const node = content.current
    if (!node) return
    const read = () => {
      setNatural(node.offsetHeight)
      onHeight(t.id, node.offsetHeight)
    }
    read()
    const ro = new ResizeObserver(read)
    ro.observe(node)
    return () => ro.disconnect()
  }, [t.id, onHeight])

  // 登记到列表的热区计算里
  const entry = React.useRef({ extent: () => (typeof height.get() === "number" ? (height.get() as number) : natural) - shownY.get(), visible })
  entry.current.visible = visible && isPresent
  React.useLayoutEffect(() => {
    registry.set(t.id, entry.current)
    const off = [shownY.on("change", measure), height.on("change", measure)]
    return () => {
      registry.delete(t.id)
      off.forEach((f) => f())
      measure()
      onHeight(t.id, -1)
    }
  }, [registry, t.id, shownY, height, measure, onHeight])

  // 位置、缩放、高度、透明度的目标由在叠里的位置决定
  const collapsed = !expanded && index > 0
  const target = {
    y: expanded ? -offset : -PEEK * index,
    scale: expanded ? 1 : 1 - SHRINK * index,
    height: collapsed ? front : natural,
    opacity: visible ? 1 : 0,
  }
  React.useLayoutEffect(() => {
    if (!isPresent || flying.current || !natural) return
    const set = (mv: MotionValue<number> | MotionValue<number | "auto">, to: number, spring: object) =>
      reduce ? (mv as MotionValue<number>).jump(to) : animate(mv as MotionValue<number>, to, spring)
    if (!entered.current) {
      entered.current = true
      // 出现：从自己一个身位之下升起，灵动岛式落定
      height.jump(target.height)
      scale.jump(target.scale)
      // 起点同步写上：motion 的第一帧要等下一次刷新，不先写的话这一帧提示会停在终点（热区闪一下全高）
      y.jump(reduce ? target.y : natural)
      if (!reduce) animate(y, target.y, SPRINGS.bouncy)
      animate(opacity, target.opacity, reduce ? { duration: DUR.fast, ease: EASE_OUT } : SPRINGS.smooth)
      return
    }
    set(y, target.y, SPRINGS.smooth)
    set(scale, target.scale, SPRINGS.smooth)
    set(height, target.height, SPRINGS.smooth)
    animate(opacity, target.opacity, { duration: DUR.fast, ease: EASE_OUT })
  }, [isPresent, natural, target.y, target.scale, target.height, target.opacity, reduce]) // eslint-disable-line react-hooks/exhaustive-deps

  // 离开：下沉一个身位并淡出（滑走的那条已经在飞，等它淡完）
  React.useLayoutEffect(() => {
    if (isPresent) return
    if (!autoClosed.current) t.onDismiss?.(t)
    if (flying.current) return void flying.current.then(() => safeToRemove?.())
    const fade = animate(opacity, 0, { duration: DUR.fast, ease: EASE_OUT })
    if (!reduce) animate(y, y.get() + natural, { duration: DUR.fast, ease: EASE_OUT })
    fade.then(() => safeToRemove?.())
  }, [isPresent]) // eslint-disable-line react-hooks/exhaustive-deps

  // 停留计时：悬停、按住、页面在后台时暂停，剩余时间接着算；等待中的不计时
  const remaining = React.useRef(duration)
  React.useEffect(() => {
    remaining.current = duration
  }, [duration, t.type])
  React.useEffect(() => {
    if (t.type === "loading" || duration === Infinity || paused || !isPresent) return
    const start = Date.now()
    const timer = window.setTimeout(() => {
      autoClosed.current = true
      t.onAutoClose?.(t)
      sonner.dismiss(t.id)
    }, remaining.current)
    return () => {
      window.clearTimeout(timer)
      remaining.current -= Date.now() - start
    }
  }, [paused, duration, t, isPresent])

  const swipe = useSwipe({ toast: t, swipeX, swipeY, opacity, natural, reduce, onFly: (fade) => (flying.current = fade) })

  const type = t.type ?? "default"
  const title = typeof t.title === "function" ? t.title() : t.title
  const description = typeof t.description === "function" ? t.description() : t.description
  const icon = t.icon ?? icons?.[type as keyof NonNullable<typeof icons>] ?? ICONS[type]
  const button = (a: Action | React.ReactNode) =>
    isAction(a) ? (
      <button
        type="button"
        data-slot="toast-action"
        style={a.actionButtonStyle}
        className={cn(
          "-mr-3 ml-auto h-(--ds-h-sm) shrink-0 rounded-control px-2 text-sm font-medium text-fg outline-none first-of-type:ml-auto [&+&]:ml-0",
          "transition-colors duration-(--ds-dur-fast) ease-ds hover:bg-hover focus-visible:ring-2 focus-visible:ring-focus/40"
        )}
        onClick={(e) => {
          a.onClick(e)
          if (!e.defaultPrevented) sonner.dismiss(t.id)
        }}
      >
        {a.label}
      </button>
    ) : (
      a
    )

  return (
    <motion.li
      data-slot="toast"
      data-type={type}
      data-front={index === 0}
      data-paused={paused}
      data-swiping={swipe.active}
      aria-hidden={!visible || undefined}
      className={cn(
        "absolute inset-x-0 bottom-0 origin-top touch-none overflow-hidden rounded-popover bg-card text-sm text-fg shadow-popover select-none",
        !visible && "pointer-events-none",
        // 进度线：底边 1px，落在左右 16 的内容线之间，停留时长内匀速从右往左收短；暂停时一起停
        "before:absolute before:inset-x-4 before:bottom-0 before:h-px before:origin-left before:bg-line-strong",
        "before:animate-[ds-toast-progress_var(--ds-toast-duration)_linear_forwards] data-[paused=true]:before:[animation-play-state:paused]",
        "data-[type=loading]:before:hidden data-[swiping=true]:before:hidden motion-reduce:before:hidden",
        t.className
      )}
      style={{
        ...t.style,
        y: shownY,
        x: swipeX,
        scale,
        opacity,
        height,
        zIndex: count - index,
        ["--ds-toast-duration" as string]: `${duration}ms`,
      }}
      {...swipe.handlers}
    >
      <div
        ref={content}
        className={cn(
          "relative transition-opacity duration-(--ds-dur-fast) ease-ds",
          !t.jsx && "flex min-h-(--ds-h-lg) items-center gap-2 px-4 py-2",
          collapsed && "opacity-0"
        )}
      >
        {t.jsx ?? (
          <>
            <AnimatePresence mode="popLayout" initial={false}>
              <motion.div
                key={type}
                className="flex min-w-0 flex-1 items-center gap-2"
                initial={{ opacity: 0, filter: reduce ? "blur(0px)" : "blur(4px)" }}
                animate={{ opacity: 1, filter: "blur(0px)" }}
                exit={{ opacity: 0, filter: reduce ? "blur(0px)" : "blur(4px)" }}
                transition={{ duration: DUR.fast, ease: EASE_OUT }}
              >
                {icon ? <span className="flex size-4 shrink-0 items-center justify-center [&_svg]:size-4">{icon}</span> : null}
                <div className="grid min-w-0">
                  <div data-slot="toast-title">{title}</div>
                  {description ? <div className="text-fg-muted">{description}</div> : null}
                </div>
              </motion.div>
            </AnimatePresence>
            {t.cancel ? button(t.cancel) : null}
            {t.action ? button(t.action) : null}
          </>
        )}
      </div>
    </motion.li>
  )
}

/**
 * 滑走：横向随意、向下 1:1 跟手；向上按橡皮筋。松手够远或够快就带着速度飞出去，否则带着速度弹回。
 * 按在按钮上不算拖；选中了文字不拖；等待中的、不可关闭的提示不能拖。
 */
function useSwipe({
  toast: t,
  swipeX,
  swipeY,
  opacity,
  natural,
  reduce,
  onFly,
}: {
  toast: ToastT
  swipeX: MotionValue<number>
  swipeY: MotionValue<number>
  opacity: MotionValue<number>
  natural: number
  reduce: boolean | null
  onFly: (fade: Promise<unknown>) => void
}) {
  const drag = React.useRef<{ x: number; y: number; t: number; axis: "x" | "y" | null; trail: [number, number][] } | null>(null)
  const [active, setActive] = React.useState(false)
  const locked = t.type === "loading" || t.dismissible === false

  const onPointerDown = (e: React.PointerEvent<HTMLElement>) => {
    if (e.button !== 0 || locked || (e.target as Element).closest("button, a, input, textarea")) return
    e.currentTarget.setPointerCapture(e.pointerId)
    swipeX.stop()
    swipeY.stop()
    drag.current = { x: e.clientX - swipeX.get(), y: e.clientY - swipeY.get(), t: performance.now(), axis: null, trail: [] }
  }
  const onPointerMove = (e: React.PointerEvent<HTMLElement>) => {
    const d = drag.current
    if (!d || (window.getSelection()?.toString().length ?? 0) > 0) return
    const dx = e.clientX - d.x
    const dy = e.clientY - d.y
    if (!d.axis) {
      if (Math.abs(dx) < 2 && Math.abs(dy) < 2) return
      d.axis = Math.abs(dx) > Math.abs(dy) ? "x" : "y"
      setActive(true)
    }
    const v = d.axis === "x" ? dx : dy > 0 ? dy : rubberband(dy, 0, Infinity, natural || 40)
    ;(d.axis === "x" ? swipeX : swipeY).set(v)
    d.trail = [...d.trail.filter(([at]) => performance.now() - at < 80), [performance.now(), v]]
  }
  const onPointerUp = () => {
    const d = drag.current
    drag.current = null
    if (!d?.axis) return
    setActive(false)
    const mv = d.axis === "x" ? swipeX : swipeY
    const amount = mv.get()
    const first = d.trail[0]
    const last = d.trail.at(-1)
    // 松手那一刻的速度（px/s），交给弹簧接着走
    const velocity = first && last && last[0] > first[0] ? ((last[1] - first[1]) / (last[0] - first[0])) * 1000 : 0
    const average = Math.abs(amount) / (performance.now() - d.t)
    const allowed = d.axis === "x" || amount > 0
    if (allowed && (Math.abs(amount) >= SWIPE_DISTANCE || average > SWIPE_VELOCITY)) {
      const away = d.axis === "x" ? Math.sign(amount) * 400 : natural + 64
      if (reduce) mv.jump(away)
      else animate(mv, away, { ...SPRINGS.smooth, velocity })
      const fade = animate(opacity, 0, { duration: DUR.fast, ease: EASE_OUT })
      onFly(new Promise((done) => void fade.then(() => done(null))))
      sonner.dismiss(t.id)
      return
    }
    if (reduce) mv.jump(0)
    else animate(mv, 0, { ...SPRINGS.bouncy, velocity })
  }
  return { active, handlers: { onPointerDown, onPointerMove, onPointerUp, onPointerCancel: onPointerUp } }
}

const ICONS: Record<string, React.ReactNode> = {
  loading: <Spinner />,
  success: <CircleCheck className="text-success" />,
  info: <Info className="text-fg-subtle" />,
  warning: <TriangleAlert className="text-warning" />,
  error: <CircleAlert className="text-danger" />,
}

function isAction(a: unknown): a is Action {
  return typeof a === "object" && a !== null && "label" in a && "onClick" in a
}

function useDocumentHidden() {
  return React.useSyncExternalStore(
    (on) => {
      document.addEventListener("visibilitychange", on)
      return () => document.removeEventListener("visibilitychange", on)
    },
    () => document.hidden,
    () => false
  )
}

export { Toaster }
