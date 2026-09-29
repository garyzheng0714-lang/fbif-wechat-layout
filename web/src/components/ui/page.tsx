import * as React from "react"

import { cn } from "@/lib/utils"

/**
 * 页面列（DESIGN.md §3.1 Page）：一列内容，固定最大宽度、水平居中；顶栏下 104 起标题、页底留 128。
 * doc 864（正文 672 + 两侧 96）用于阅读、规范、目录；wide 1344 用于画布与多栏示例。
 * 窄屏左右各 24，宽度不按百分比伸缩（L&S《网格被高估》）。
 */
function Page({ width = "doc", className, ...props }: React.ComponentProps<"main"> & { width?: "doc" | "wide" }) {
  return (
    <main
      data-slot="page"
      className={cn(
        "mx-auto w-full min-w-0 px-6 pt-(--ds-page-top) pb-(--ds-page-bottom) sm:px-24",
        width === "wide" ? "max-w-(--ds-page-wide)" : "max-w-(--ds-page-doc)",
        className
      )}
      {...props}
    />
  )
}

/** 页面标题 40/48；字体、字重、字距取 --ds-font-display / --ds-weight-title / --ds-tracking-display。下面不加复述标题的说明。 */
function PageHeader({ title, className, children }: { title: string; className?: string; children?: React.ReactNode }) {
  return (
    <header data-slot="page-header" className={cn("grid gap-1 pb-2", className)}>
      <h1 className="font-display text-3xl tracking-(--ds-tracking-display) [font-weight:var(--ds-weight-title)]">{title}</h1>
      {children}
    </header>
  )
}

/** 分节：二级标题 24/30 半粗，上方 32、到内容 8；只用留白分组，不套卡片。 */
function Section({ title, className, children, ...props }: Omit<React.ComponentProps<"section">, "title"> & { title: string }) {
  return (
    <section data-slot="section" className={cn("grid scroll-mt-24 gap-2 pt-8", className)} {...props}>
      <h2 className="font-display text-xl tracking-(--ds-tracking-display) [font-weight:var(--ds-weight-heading)]">{title}</h2>
      {children}
    </section>
  )
}

/**
 * 顶栏（DESIGN.md §3.1 Topbar，44 高）：贴顶，左边 ☰ 与面包屑，右边 ≤3 个图标按钮（最后一个用 edge-end）。
 * 顶栏放在圆角的纸或卡片顶上时，最后一个按钮改成嵌进角里：右内边距 4（pr-1），不用 edge-end，上下右间距相等、圆角同心（corner-audit）。
 * 页面标题不放这里，标题属于 Page。不画底线：滚动时下面的内容渐进模糊着没入栏下（progressive-blur）。
 */
function Topbar({ className, children, ...props }: React.ComponentProps<"header">) {
  return (
    <header data-slot="topbar" className={cn("sticky top-0 z-30 isolate flex h-(--ds-header-h) shrink-0 items-center px-3", className)} {...props}>
      {/* 静态摆放（示例、模板里的内嵌顶栏）下面没有滚动内容，不需要模糊 */}
      <div aria-hidden className="progressive-blur [.static>&]:hidden" />
      {children}
    </header>
  )
}

export { Page, PageHeader, Section, Topbar }
