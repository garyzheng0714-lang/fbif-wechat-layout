"use client"

import * as React from "react"
import { Check, ChevronRight } from "lucide-react"
import { AnimatePresence } from "motion/react"
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { Keys } from "@/components/ui/kbd"

import { fromKeyboard } from "@/components/ui/hotkeys"
import { PopupContext, PopupMotion, useOpenState, usePopup } from "@/components/ui/popup"

/**
 * 下拉菜单：最小宽 176，内边距 4，圆角 10；菜单项高 28、左右 8、圆角 6（同心）；
 * 图标 18、深色，与文字间距 8（文字起点 38）；快捷键是右侧 12 号灰字，不画键帽。
 * 指针或键盘停留的项 bg-hover；超过 7 项用分隔线和小标题分组。
 * select 等组件复用同一套样式。
 * 动效（popup.tsx）：点击触发 → 以触发元素为原点（Radix 给的 transform-origin）scale .95→1 + 透明度，SPRINGS.snappy（0.22 / 0.15）；
 * 关闭 150ms ease-out 原路缩回；子菜单从父菜单项旁边同样长出来；菜单项不错落。
 * 键盘打开 / Esc 关闭 0ms；减少动态效果只淡入淡出。
 */
const dropdownMenuSurface =
  "min-w-44 overflow-hidden rounded-popover bg-card p-(--ds-pad-popover) text-sm text-fg shadow-popover"
const dropdownMenuItemClass = [
  "relative flex h-(--ds-h-md) cursor-default items-center gap-2 rounded-popover-item px-2 outline-none select-none",
  "text-fg data-highlighted:bg-hover data-disabled:pointer-events-none data-disabled:opacity-40",
  "[&_svg]:size-4.5 [&_svg]:shrink-0 [&_svg]:text-fg",
]

const origin = "z-50 origin-(--radix-dropdown-menu-content-transform-origin)"
const grow = { scale: 0.95 }

function DropdownMenu({ open, defaultOpen, onOpenChange, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Root>) {
  const state = useOpenState(open, defaultOpen, onOpenChange)
  return (
    <PopupContext value={state}>
      <DropdownMenuPrimitive.Root {...props} open={state.open} onOpenChange={state.setOpen} />
    </PopupContext>
  )
}

const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger

function DropdownMenuContent({
  className,
  align = "end",
  sideOffset = 8,
  children,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Content>) {
  const { open } = usePopup()
  return (
    <AnimatePresence custom={fromKeyboard()}>
      {open ? (
        <DropdownMenuPrimitive.Portal forceMount>
          <DropdownMenuPrimitive.Content forceMount asChild align={align} sideOffset={sideOffset} collisionPadding={8} {...props}>
            <PopupMotion data-slot="dropdown-menu-content" from={grow} className={cn(dropdownMenuSurface, origin, className)}>
              {children}
            </PopupMotion>
          </DropdownMenuPrimitive.Content>
        </DropdownMenuPrimitive.Portal>
      ) : null}
    </AnimatePresence>
  )
}

function DropdownMenuItem({
  className,
  shortcut,
  tone = "default",
  children,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Item> & {
  shortcut?: string
  tone?: "default" | "danger"
}) {
  return (
    <DropdownMenuPrimitive.Item
      className={cn(dropdownMenuItemClass, tone === "danger" && "[&_svg]:text-danger", className)}
      {...props}
    >
      {children}
      {shortcut ? <Keys keys={shortcut} className="ml-auto pl-4 text-xs text-fg-subtle" /> : null}
    </DropdownMenuPrimitive.Item>
  )
}

function DropdownMenuLabel({ className, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Label>) {
  return (
    <DropdownMenuPrimitive.Label className={cn("px-2 pt-2 pb-1 text-xs font-medium text-fg-subtle", className)} {...props} />
  )
}

function DropdownMenuSeparator({ className, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Separator>) {
  return (
    <DropdownMenuPrimitive.Separator
      className={cn("-mx-(--ds-pad-popover) my-1 h-px bg-line", className)}
      {...props}
    />
  )
}

const DropdownMenuGroup = DropdownMenuPrimitive.Group

function DropdownMenuSub({ open, defaultOpen, onOpenChange, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Sub>) {
  const state = useOpenState(open, defaultOpen, onOpenChange)
  return (
    <PopupContext value={state}>
      <DropdownMenuPrimitive.Sub {...props} open={state.open} onOpenChange={state.setOpen} />
    </PopupContext>
  )
}

/** 复选项：勾号在右侧，不用彩色底表示已勾选。 */
function DropdownMenuCheckboxItem({
  className,
  children,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.CheckboxItem>) {
  return (
    <DropdownMenuPrimitive.CheckboxItem className={cn(dropdownMenuItemClass, className)} {...props}>
      {children}
      <span className="ml-auto flex size-4 items-center justify-center">
        <DropdownMenuPrimitive.ItemIndicator>
          <Check className="text-fg!" />
        </DropdownMenuPrimitive.ItemIndicator>
      </span>
    </DropdownMenuPrimitive.CheckboxItem>
  )
}

function DropdownMenuSubTrigger({
  className,
  children,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.SubTrigger>) {
  return (
    <DropdownMenuPrimitive.SubTrigger
      className={cn(dropdownMenuItemClass, "data-[state=open]:bg-hover", className)}
      {...props}
    >
      {children}
      <ChevronRight className="ml-auto" />
    </DropdownMenuPrimitive.SubTrigger>
  )
}

/** 子菜单从父菜单项旁边展开，收起时缩回原处。 */
function DropdownMenuSubContent({
  className,
  sideOffset = 4,
  alignOffset = -4,
  children,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.SubContent>) {
  const { open } = usePopup()
  return (
    <AnimatePresence custom={fromKeyboard()}>
      {open ? (
        <DropdownMenuPrimitive.Portal forceMount>
          <DropdownMenuPrimitive.SubContent forceMount asChild sideOffset={sideOffset} alignOffset={alignOffset} collisionPadding={8} {...props}>
            <PopupMotion data-slot="dropdown-menu-sub-content" from={grow} className={cn(dropdownMenuSurface, origin, className)}>
              {children}
            </PopupMotion>
          </DropdownMenuPrimitive.SubContent>
        </DropdownMenuPrimitive.Portal>
      ) : null}
    </AnimatePresence>
  )
}

export {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
  dropdownMenuItemClass,
  dropdownMenuSurface,
}
