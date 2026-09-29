import * as React from "react"
import { Slot } from "radix-ui"
import { ChevronRight } from "lucide-react"

import { cn } from "@/lib/utils"
import { LiftedCard } from "@/components/ui/lifted-card"
import { SidebarContext } from "@/components/ui/sidebar"
import { Tooltip } from "@/components/ui/tooltip"

/**
 * 导航项：高 28、行与行之间 2 的缝（行距仍是 30，相邻底色不粘连）、左右内边距 8、图标 18、图标与文字间距 6、圆角 6、字号 14、字重 --ds-weight-nav（400）。当前项同一字重，靠底色与墨色区分（不改字重，切换时文字不跳）。
 * 平时无底色，悬停 bg-hover；当前项直接出现（不滑动）、文字变 fg：和底色反着来——在灰侧栏上是白色凸起小卡（--base-nav-lift）。图标默认 fg-subtle，悬停或当前时 fg。悬停与当前都即时，不做渐变。
 * asChild：渲染为路由链接等元素，如 <NavItem asChild><Link to="/">首页</Link></NavItem>。
 * collapse="rail" 侧栏收起时只显示图标，名称进悬停提示（需要 icon）。
 */
function NavItem({
  className,
  icon,
  count,
  active = false,
  group = "nav",
  tooltip,
  asChild = false,
  onClick,
  children,
  ...props
}: React.ComponentProps<"button"> & {
  icon?: React.ReactNode
  count?: number
  active?: boolean
  /** 同一侧栏的导航项共用一个 group */
  group?: string
  /** 图标栏里的悬停提示；children 是纯文字时可省略 */
  tooltip?: string
  asChild?: boolean
}) {
  const sidebar = React.useContext(SidebarContext)
  const collapsed = Boolean(sidebar && sidebar.desktop && sidebar.collapse === "rail" && !sidebar.open)
  const Comp = asChild ? Slot.Root : "button"

  const item = (
    <Comp
      type={asChild ? undefined : "button"}
      aria-current={active ? "page" : undefined}
      onClick={(e: React.MouseEvent<HTMLButtonElement>) => {
        onClick?.(e)
        // 窄屏下侧栏是浮层：选中后自动收起
        if (sidebar && !sidebar.desktop) sidebar.setMobileOpen(false)
      }}
      className={cn(
        "relative isolate flex h-(--ds-h-row) w-full min-w-0 items-center gap-1.5 rounded-row px-2 text-left text-sm [font-weight:var(--ds-weight-nav)] text-fg-muted outline-none",
        "hover:text-fg",
        "not-aria-[current=page]:hover:bg-hover",
        "focus-visible:ring-2 focus-visible:ring-focus/40",
        "aria-[current=page]:text-fg",
        "disabled:pointer-events-none disabled:opacity-40 aria-disabled:pointer-events-none aria-disabled:opacity-40",
        "[&_svg]:size-4.5 [&_svg]:shrink-0 [&_svg]:text-fg-subtle",
        "hover:[&_svg]:text-fg aria-[current=page]:[&_svg]:text-fg",
        collapsed && "w-(--ds-h-row) justify-center px-0 [&>:not(svg,[data-slot=lift])]:sr-only",
        className
      )}
      {...props}
    >
      {active ? <LiftedCard group={group} className="-z-10 rounded-row bg-nav-current shadow-nav-current" /> : null}
      {icon}
      {asChild ? <Slot.Slottable>{children}</Slot.Slottable> : <span className="min-w-0 flex-1 truncate">{children}</span>}
      {count ? <span className="ml-auto text-xs font-normal text-fg-subtle tabular-nums">{count}</span> : null}
    </Comp>
  )

  if (!collapsed) return item
  const label = tooltip ?? (typeof children === "string" ? children : undefined)
  return label ? (
    <Tooltip content={label} side="right">
      {item}
    </Tooltip>
  ) : (
    item
  )
}

/** 导航分组：组名（12、字重 500、fg-subtle，占一行）+ 导航项，项与项之间 2 的缝（行距 30）。组间靠留白分隔；图标栏收起时组名隐藏。 */
function NavSection({
  label,
  action,
  className,
  children,
}: {
  label?: React.ReactNode
  action?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  const sidebar = React.useContext(SidebarContext)
  const collapsed = Boolean(sidebar && sidebar.desktop && sidebar.collapse === "rail" && !sidebar.open)
  return (
    <div role="group" className={cn("grid gap-(--ds-gap-row)", className)}>
      {label && !collapsed ? (
        <div className="flex h-(--ds-h-row) items-center justify-between px-2 text-xs font-medium text-fg-subtle">
          <span className="truncate">{label}</span>
          {action}
        </div>
      ) : null}
      {children}
    </div>
  )
}

/**
 * 可展开的一级目录（页面树）：一行导航项，悬停时图标位换成 ›，点一下展开或收起。
 * 展开后子项缩进 24、不带图标：子项文字正好对齐父项文字，层级靠这条线看出来。展开收起即时，不做高度动画。
 */
function NavFolder({
  label,
  icon,
  open,
  onOpenChange,
  children,
}: {
  label: string
  icon?: React.ReactNode
  open: boolean
  onOpenChange: (open: boolean) => void
  children: React.ReactNode
}) {
  const id = React.useId()
  const sidebar = React.useContext(SidebarContext)
  const collapsed = Boolean(sidebar && sidebar.desktop && sidebar.collapse === "rail" && !sidebar.open)
  const chevron = <ChevronRight aria-hidden className={cn(open && "rotate-90")} />
  return (
    <div className="grid gap-(--ds-gap-row)">
      <NavItem
        aria-expanded={open}
        aria-controls={id}
        tooltip={label}
        onClick={() => onOpenChange(!open)}
        className="group/folder"
        icon={
          icon ? (
            <span className="grid size-4.5 shrink-0 *:col-start-1 *:row-start-1">
              <span className="grid group-hover/folder:invisible group-focus-visible/folder:invisible">{icon}</span>
              <span className="invisible grid group-hover/folder:visible group-focus-visible/folder:visible">{chevron}</span>
            </span>
          ) : (
            chevron
          )
        }
      >
        {label}
      </NavItem>
      {open && !collapsed ? (
        <div id={id} role="group" aria-label={label} className="grid gap-(--ds-gap-row) pl-6">
          {children}
        </div>
      ) : null}
    </div>
  )
}

export { NavFolder, NavItem, NavSection }
