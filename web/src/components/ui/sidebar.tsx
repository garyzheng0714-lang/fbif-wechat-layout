"use client"

import * as React from "react"
import { PanelLeft, PanelRight } from "lucide-react"
import { animate, motion, useMotionValue, useReducedMotion, useTransform, type MotionValue } from "motion/react"
import { Dialog as DialogPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { SPRINGS } from "@/components/ui/ease"
import { useHotkeys } from "@/components/ui/hotkeys"

import { Button } from "@/components/ui/button"
import { Tooltip } from "@/components/ui/tooltip"

/**
 * 侧栏：宽 256（放表单卡片的工具栏 400，见 SidebarProvider.width），左右内边距 8，组间 12。两种收起方式：
 * - collapse="rail"：功能导航（项少、有图标），收成 56 图标栏，名称进悬停提示。
 * - collapse="hide"：目录导航（项多、纯文字），完全隐藏；指针碰到屏幕左边缘时浮出。
 * ⌘\ / Ctrl+\ 切换。窄屏（<1024px）一律变为从左侧滑出的浮层。
 * 动效（docs/研究/动效-苹果.md §5）：
 * - 点收起按钮开合 → 图标栏宽度 / 占位列宽度与面板平移同一条 SPRINGS.smooth（0.35 / 0，位移 >200px 不过冲），可打断，中途反向从当前位置接着走。
 * - 指针碰左边缘浮出、离开收回 → 面板平移 SPRINGS.smooth。
 * - 键盘（⌘\、键盘按下收起按钮、Esc 收回浮出）→ 0ms 直接到位（DESIGN.md §5.1）。
 * - 窄屏浮层是一次性进出场，走 CSS：进 300 ease-ds、出 200；键盘开合时同样 0ms。
 * - 减少动态效果 → 宽度、平移直接到位。
 */
type Collapse = "hide" | "rail"

type SidebarContextValue = {
  collapse: Collapse
  open: boolean
  desktop: boolean
  mobileOpen: boolean
  setMobileOpen: (open: boolean) => void
  peek: boolean
  setPeek: (peek: boolean) => void
  /** instant：键盘触发的开合，不做动画 */
  toggle: (opts?: { instant?: boolean }) => void
  /** 这一次开合要不要跳过动画；由 toggle 写入，这一次提交之后清掉 */
  instant: React.RefObject<boolean>
}

const SidebarContext = React.createContext<SidebarContextValue | null>(null)

function useSidebar() {
  const ctx = React.useContext(SidebarContext)
  if (!ctx) throw new Error("useSidebar 必须在 <SidebarProvider> 里使用")
  return ctx
}

const DESKTOP_QUERY = "(min-width: 1024px)"

/**
 * 侧栏宽度（DESIGN.md §3.2 宽度阶梯）：256 放导航（默认）；400 给「工具栏」——侧栏里放的是导入、参数这类表单卡片，
 * 256 摆不下一行输入加动作。只有这两档，别的宽度先改 DESIGN.md。
 */
type SidebarWidth = 256 | 400

const SIDEBAR_WIDTH: Record<SidebarWidth, string | undefined> = {
  256: undefined,
  400: "[--ds-sidebar-w:calc(var(--spacing)*100)]",
}

function useDesktop() {
  return React.useSyncExternalStore(
    (onChange) => {
      const mq = matchMedia(DESKTOP_QUERY)
      mq.addEventListener("change", onChange)
      return () => mq.removeEventListener("change", onChange)
    },
    () => matchMedia(DESKTOP_QUERY).matches,
    () => true
  )
}

function SidebarProvider({
  collapse = "hide",
  width = 256,
  defaultOpen = true,
  open: openProp,
  onOpenChange,
  shortcut = true,
  className,
  children,
}: {
  collapse?: Collapse
  /** 展开时的宽度，只取宽度阶梯里的 256 / 400 */
  width?: SidebarWidth
  defaultOpen?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
  /** 是否响应 ⌘\ / Ctrl+\。一个页面里只让主侧栏响应，其余（例如演示）传 false */
  shortcut?: boolean
  className?: string
  children: React.ReactNode
}) {
  const [uncontrolled, setUncontrolled] = React.useState(defaultOpen)
  const open = openProp ?? uncontrolled
  const desktop = useDesktop()
  const [mobileOpen, setMobileOpen] = React.useState(false)
  const [peek, setPeek] = React.useState(false)
  const instant = React.useRef(false)
  // 子组件的 layout effect 先于这里执行：它们读完这一次的 instant，再清掉
  React.useLayoutEffect(() => {
    instant.current = false
  })

  const toggle = React.useCallback((opts: { instant?: boolean } = {}) => {
    instant.current = Boolean(opts.instant)
    if (!desktop) return setMobileOpen((v) => !v)
    const next = !open
    if (openProp === undefined) setUncontrolled(next)
    onOpenChange?.(next)
    setPeek(false)
  }, [desktop, open, openProp, onOpenChange])

  useHotkeys({ "mod+\\": () => toggle({ instant: true }) }, { enabled: shortcut })

  return (
    <SidebarContext.Provider value={{ collapse, open, desktop, mobileOpen, setMobileOpen, peek, setPeek, toggle, instant }}>
      <div data-slot="sidebar-wrapper" className={cn("flex min-h-svh w-full bg-canvas text-fg", SIDEBAR_WIDTH[width], className)}>
        {children}
      </div>
    </SidebarContext.Provider>
  )
}

const panelClass = "group/sidebar flex w-(--ds-sidebar-w) flex-col text-fg"
const floatingClass = "top-(--ds-gutter) bottom-(--ds-gutter) left-(--ds-gutter) rounded-card bg-card shadow-overlay"

/**
 * 把开合进度 p（0–1）推到 to：SPRINGS.smooth；首帧、键盘触发、减少动态效果时直接到位。onRest 只在自然停稳时调用（被打断不算）。
 * 必须写在 p 的 useTransform 之后：useTransform 每次渲染都在 layout effect 里重新订阅，写在它前面时这一帧 set 的值它收不到。
 */
function useSpringTo(p: MotionValue<number>, to: number, onRest?: () => void) {
  const { instant } = useSidebar()
  const reduce = useReducedMotion()
  const first = React.useRef(true)
  React.useLayoutEffect(() => {
    if (first.current || reduce || instant.current) {
      first.current = false
      p.stop()
      p.set(to)
      onRest?.()
      return
    }
    // 进度是 0–1 的小数，停稳阈值按 256px 宽换算到 0.1px 以内，免得最后一帧跳一下
    animate(p, to, { ...SPRINGS.smooth, restDelta: 0.0004, restSpeed: 0.004, onComplete: onRest })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [to])
}

function Sidebar({ className, label = "侧栏", children }: { className?: string; label?: string; children: React.ReactNode }) {
  const { collapse, desktop, mobileOpen, setMobileOpen, instant } = useSidebar()

  if (!desktop) {
    return (
      <DialogPrimitive.Root open={mobileOpen} onOpenChange={setMobileOpen}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay
            className={cn(
              "fixed inset-0 z-50 bg-scrim duration-(--ds-dur-slow) ease-ds data-[state=closed]:duration-(--ds-dur-base)",
              "data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0",
              instant.current && "animate-none!"
            )}
          />
          <DialogPrimitive.Content
            aria-describedby={undefined}
            className={cn(
              panelClass,
              floatingClass,
              "fixed z-50 max-w-[calc(100%-48px)] outline-none duration-(--ds-dur-slow) ease-ds data-[state=closed]:duration-(--ds-dur-base)",
              "[--ds-off:calc(-100%_-_var(--ds-gutter))] [--tw-enter-translate-x:var(--ds-off)] [--tw-exit-translate-x:var(--ds-off)]",
              "data-[state=open]:animate-in data-[state=closed]:animate-out",
              instant.current && "animate-none!",
              className
            )}
          >
            <DialogPrimitive.Title className="sr-only">{label}</DialogPrimitive.Title>
            {children}
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
    )
  }

  if (collapse === "rail") return <RailSidebar className={className} label={label}>{children}</RailSidebar>
  return <HiddenSidebar className={className} label={label}>{children}</HiddenSidebar>
}

/** collapse="rail" 的宽屏形态：宽度在图标栏与展开之间走弹簧。 */
function RailSidebar({ className, label, children }: { className?: string; label: string; children: React.ReactNode }) {
  const { open } = useSidebar()
  const p = useMotionValue(open ? 1 : 0)
  const width = useTransform(p, (v) => `calc(var(--ds-rail-w) + (var(--ds-sidebar-w) - var(--ds-rail-w)) * ${v})`)
  useSpringTo(p, open ? 1 : 0)
  return (
    <motion.aside
      aria-label={label}
      data-collapsed={!open || undefined}
      style={{ width }}
      className={cn("group/sidebar sticky top-0 flex h-svh shrink-0 flex-col overflow-hidden bg-sidebar text-fg", className)}
    >
      {children}
    </motion.aside>
  )
}

/** collapse="hide" 的宽屏形态：占位列挤开正文，面板固定定位，收起后可从左边缘浮出。 */
function HiddenSidebar({ className, label, children }: { className?: string; label: string; children: React.ReactNode }) {
  const { open, peek, setPeek, instant } = useSidebar()
  const [floating, setFloating] = React.useState(false)
  const timer = React.useRef<number | undefined>(undefined)
  const shown = open || peek

  const schedule = (fn: () => void, ms: number) => {
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(fn, ms)
  }
  React.useEffect(() => () => window.clearTimeout(timer.current), [])
  React.useEffect(() => {
    if (open) setFloating(false)
  }, [open])
  // 占位列跟 open 走，面板跟 shown 走（浮出时占位列不动，正文不被挤开）；面板收回停稳后才换回贴边的样子
  const column = useMotionValue(open ? 1 : 0)
  const panel = useMotionValue(shown ? 1 : 0)
  const columnWidth = useTransform(column, (v) => `calc(var(--ds-sidebar-w) * ${v})`)
  const transform = useTransform(panel, (v) => `translateX(calc((-100% - 16px) * ${1 - v}))`)
  useSpringTo(column, open ? 1 : 0)
  useSpringTo(panel, shown ? 1 : 0, shown ? undefined : () => setFloating(false))

  // 碰到左边缘先等 80ms，扫过去的指针不会误触发；离开后留 300ms 余量，斜着移回来也不会收走。
  const enterEdge = () =>
    schedule(() => {
      setFloating(true)
      setPeek(true)
    }, 80)
  const leavePanel = () => {
    if (!open) schedule(() => setPeek(false), 300)
  }

  return (
    <>
      <motion.div aria-hidden style={{ width: columnWidth }} className="shrink-0" />
      {!open ? (
        <div aria-hidden className="fixed inset-y-0 left-0 z-40 w-2" onMouseEnter={enterEdge} onMouseLeave={() => window.clearTimeout(timer.current)} />
      ) : null}
      <motion.aside
        aria-label={label}
        inert={!shown}
        style={{ transform }}
        onMouseEnter={() => window.clearTimeout(timer.current)}
        onMouseLeave={leavePanel}
        onKeyDown={(e) => {
          if (e.key === "Escape" && peek) {
            instant.current = true
            setPeek(false)
          }
        }}
        className={cn(panelClass, "fixed z-40", floating ? floatingClass : "inset-y-0 left-0 bg-sidebar", className)}
      >
        {children}
      </motion.aside>
    </>
  )
}

function SidebarHeader({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="sidebar-header" className={cn("flex h-(--ds-header-h) shrink-0 items-center gap-2 px-4 group-data-[collapsed]/sidebar:px-4", className)} {...props} />
}

/** 滚动区：组间 16 留白。 */
function SidebarContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-content"
      className={cn("grid min-h-0 flex-1 content-start gap-3 overflow-y-auto overscroll-contain px-2 pt-2 pb-6 fade-bottom", className)}
      {...props}
    />
  )
}

function SidebarFooter({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="sidebar-footer" className={cn("grid shrink-0 gap-px px-2 pb-2", className)} {...props} />
}

/** 正文区：侧栏右侧的一切。 */
function SidebarInset({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="sidebar-inset" className={cn("flex min-w-0 flex-1 flex-col", className)} {...props} />
}

function SidebarTrigger({ className, ...props }: React.ComponentProps<typeof Button>) {
  const { open, desktop, mobileOpen, toggle } = useSidebar()
  const shown = desktop ? open : mobileOpen
  return (
    <Tooltip content={shown ? "收起侧栏" : "展开侧栏"} shortcut="⌘\">
      <Button
        variant="ghost"
        size="icon"
        aria-label={shown ? "收起侧栏" : "展开侧栏"}
        aria-expanded={shown}
        // 键盘按下（Enter / 空格，detail 为 0）的开合不做动画
        onClick={(e) => toggle({ instant: e.detail === 0 })}
        // 面板图标，不用 « 箭头（用户 2026-09-28）；开着和收起不能是同一个图标：开着 PanelLeft（左边有一栏），收起后 PanelRight
        className={className}
        {...props}
      >
        {shown ? <PanelLeft /> : <PanelRight />}
      </Button>
    </Tooltip>
  )
}

export {
  Sidebar,
  SidebarContent,
  SidebarContext,
  SidebarFooter,
  SidebarHeader,
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
}
export type { SidebarWidth }
