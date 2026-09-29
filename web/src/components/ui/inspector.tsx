"use client"

import * as React from "react"
import { AnimatePresence, animate, motion, useMotionValue, useReducedMotion, useTransform, useVelocity } from "motion/react"

import { cn } from "@/lib/utils"
import { DUR, EASE_OUT, SPRINGS } from "@/components/ui/ease"
import { fromKeyboard } from "@/components/ui/hotkeys"
import { SplitPane } from "@/components/ui/split"

/**
 * 检查器（DESIGN.md §3.1 Inspector）：画布右侧的定宽参数面板，宽 240（常规）或 300（控件多、要放长选择器）。
 * 本身是一栏 SplitPane：放进 Split（默认 gap）时是一张白卡，自己滚动。floating：浮在画布上的面板，一张浮起的卡。
 * 内容线左右各 16；分组之间 1px 线，分组标题 12 灰色大写。
 * 参数行 28 高，三列：标签（左）· 控件（中间，占满剩余）· 数值（右，等宽数字右对齐，宽 40）。
 * 参数改了只换数值，行高、列宽都不动。
 * 动效：数值（数字或含数字的字符串）用 NumberFlow 逐位滚动，见下。
 */
function Inspector({
  width = 240,
  floating = false,
  className,
  ...props
}: React.ComponentProps<"aside"> & { width?: 240 | 300; floating?: boolean }) {
  // 浮在画布上的面板（不在 Split 里）：一张浮起的卡，宽度由外面给，上下各 8，分组之间 1px 线
  if (floating)
    return <aside data-slot="inspector" data-inspector="" className={cn("divide-y divide-line rounded-card bg-card py-2 shadow-popover", className)} {...props} />
  return (
    <SplitPane asChild width={width} className={cn("divide-y divide-line", className)}>
      <aside data-inspector="" {...props} />
    </SplitPane>
  )
}

/**
 * 一组参数：标题行 28 高（右侧可放一个无底色图标按钮，用 edge-end），下面是参数行，行距 4。
 * 上下各 8：第一组标题的中线正好落在旁边 44 顶栏的中线上。
 */
function InspectorGroup({
  title,
  action,
  className,
  children,
  ...props
}: Omit<React.ComponentProps<"section">, "title"> & { title: React.ReactNode; action?: React.ReactNode }) {
  const id = React.useId()
  return (
    <section data-slot="inspector-group" aria-labelledby={id} className={cn("grid gap-1 px-4 py-2", className)} {...props}>
      <div className="flex h-(--ds-h-md) items-center justify-between gap-2">
        <h3 id={id} className="truncate text-xs font-medium text-fg-subtle uppercase">
          {title}
        </h3>
        {action}
      </div>
      {children}
    </section>
  )
}

/**
 * 一行参数：label 在左（宽 64，截断），children 是控件，value 是等宽数值（右对齐）。
 * 没有 value 时控件占满中间与右侧两列，末端落在右内容线上（开关、选择器、分段）。
 */
function InspectorRow({
  label,
  value,
  htmlFor,
  className,
  children,
  ...props
}: Omit<React.ComponentProps<"div">, "children"> & {
  label: React.ReactNode
  value?: React.ReactNode
  /** 控件的 id：点标签即聚焦控件 */
  htmlFor?: string
  children: React.ReactNode
}) {
  return (
    <div
      data-slot="inspector-row"
      className={cn("grid h-(--ds-h-md) grid-cols-[64px_minmax(0,1fr)_40px] items-center gap-2", className)}
      {...props}
    >
      <label htmlFor={htmlFor} className="truncate text-xs text-fg-muted select-none">
        {label}
      </label>
      <div className={cn("flex min-w-0 items-center", value === undefined && "col-span-2 justify-end")}>{children}</div>
      {value === undefined ? null : (
        <output htmlFor={htmlFor} className="truncate text-right font-mono text-xs text-fg tabular-nums">
          {typeof value === "number" || (typeof value === "string" && /\d/.test(value)) ? <NumberFlow value={value} /> : value}
        </output>
      )}
    </div>
  )
}

/**
 * 数字滚动：值变了，每一位数字各自像里程表一样滚到新数字——只动变了的位，其余不动。
 * - 方向跟着整体大小走：变大时每一位都往上滚（9 → 0 进位也往上），变小时往下滚。
 * - 每一位是一条 0–9 的竖条，位置是一个 snappy 弹簧（0.22s / bounce 0.15，物理参数）：拖滑块连续改值时从当前位置、当前速度接着滚，不跳。
 * - 滚得越快越糊：模糊量由这一位的速度算出（最多 1.2px），停下就清晰；每一位的窗口上下边缘渐隐，数字从边上淡入淡出。
 * - 位数变多：新的一位淡入并从相邻的数字滚进来；位数变少：多出的一位 150ms 淡出。其余各位靠右对齐，不挪。
 * - 非数字字符（%、.、-、,）原样显示。等宽数字（tabular-nums）下列宽恒定。
 * - 键盘改值（方向键拨滑块）与减少动态效果时直接换成新数字。读屏只读完整的值。
 * 用在 Inspector 的数值列；别的地方要显示会变的数字（计数、进度、金额）也可以直接用。
 */
const DIGITS = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 0]
const mod10 = (v: number) => ((v % 10) + 10) % 10
const numberOf = (text: string) => Number.parseFloat(text.replace(/[^\d.-]/g, ""))

function NumberFlow({ value, className }: { value: number | string; className?: string }) {
  const text = String(value)
  const num = numberOf(text)
  const prev = React.useRef(num)
  const mounted = React.useRef(false)
  const trend = Number.isNaN(num) || Number.isNaN(prev.current) || num === prev.current ? 0 : num > prev.current ? 1 : -1
  React.useEffect(() => {
    prev.current = num
    mounted.current = true
  })
  const instant = fromKeyboard()
  const chars = [...text]
  return (
    <span data-slot="number-flow" className={cn("relative inline-block whitespace-nowrap", className)}>
      <span className="sr-only">{text}</span>
      <span aria-hidden>
        <AnimatePresence initial={false} mode="popLayout" anchorX="right">
          {chars.map((c, i) => {
            const place = chars.length - i // 从右数第几位：位数变化时，已有的位保持身份
            return /\d/.test(c) ? (
              <Digit key={`d${place}`} digit={Number(c)} trend={trend} instant={instant} fresh={mounted.current} />
            ) : (
              <motion.span key={`${c}${place}`} className="inline-block" exit={{ opacity: 0, transition: FADE }}>
                {c}
              </motion.span>
            )
          })}
        </AnimatePresence>
      </span>
    </span>
  )
}

const FADE = { duration: DUR.fast, ease: EASE_OUT } as const
/**
 * 每一位是一个一行高的窗口：窗口外裁掉，上下边缘各 25% 渐隐——滚动途中数字从窗口边缘淡出淡入，而不是被一刀切开。
 * 静止时字形落在中间 50% 里，不受影响。（遮罩只取透明度，black 不是颜色。）
 */
const EDGE_FADE = "linear-gradient(transparent, black 25%, black 75%, transparent)"

function Digit({ digit, trend, instant, fresh }: { digit: number; trend: number; instant: boolean; fresh: boolean }) {
  const reduce = useReducedMotion()
  // 位置是没有上下界的实数，显示时取模：9 → 10 往上滚一格，看起来就是 9 → 0
  const target = React.useRef(fresh && trend && !reduce && !instant ? digit - trend : digit)
  const pos = useMotionValue(target.current)
  const y = useTransform(pos, (v) => `${(-mod10(v) * 100) / DIGITS.length}%`)
  const speed = useVelocity(pos)
  const filter = useTransform(speed, (v) => {
    const blur = Math.min(Math.abs(v) * 0.04, 1.2)
    return blur < 0.05 ? "none" : `blur(${blur.toFixed(2)}px)`
  })

  React.useLayoutEffect(() => {
    let delta = digit - mod10(target.current)
    if (trend > 0 && delta < 0) delta += 10
    else if (trend < 0 && delta > 0) delta -= 10
    else if (!trend && Math.abs(delta) > 5) delta -= Math.sign(delta) * 10
    if (!delta && pos.get() === target.current) return
    target.current += delta
    if (instant || reduce) pos.jump(target.current)
    else animate(pos, target.current, SPRINGS.snappy)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [digit])

  return (
    <motion.span
      data-slot="number-flow-digit"
      className="relative inline-block"
      style={{ maskImage: EDGE_FADE }}
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={instant ? { duration: 0 } : FADE}
    >
      <span className="invisible">0</span>
      <motion.span data-slot="number-flow-roll" className="absolute inset-x-0 top-0 flex flex-col" style={{ y, filter }}>
        {DIGITS.map((d, i) => (
          <span key={i}>{d}</span>
        ))}
      </motion.span>
    </motion.span>
  )
}

export { Inspector, InspectorGroup, InspectorRow, NumberFlow }
