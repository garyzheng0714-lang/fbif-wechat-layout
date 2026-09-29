"use client"

import * as React from "react"
import { Check, ChevronDown } from "lucide-react"
import { AnimatePresence } from "motion/react"
import { Select as SelectPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { dropdownMenuItemClass, dropdownMenuSurface } from "@/components/ui/dropdown-menu"
import { fieldSurface } from "@/components/ui/input"
import { fromKeyboard } from "@/components/ui/hotkeys"
import { PopupContext, PopupMotion, useOpenState, usePopup } from "@/components/ui/popup"

/**
 * 选择器：触发器外观同 Input，展开时同聚焦；面板样式同下拉菜单，从触发器展开、缩回。
 * 选中项只在右侧显示对勾，不加底色。
 * 动效（popup.tsx，同下拉菜单）：点击触发器 → 面板以触发器为原点（Radix 给的 transform-origin）scale .95→1 + 透明度，SPRINGS.snappy；
 * 选中或点外面关闭 → 150ms ease-out 原路缩回。键盘打开（回车、空格、方向键）/ Esc 关闭 0ms；减少动态效果只淡入淡出。
 */
const grow = { scale: 0.95 }

function Select({ open, defaultOpen, onOpenChange, ...props }: React.ComponentProps<typeof SelectPrimitive.Root>) {
  const state = useOpenState(open, defaultOpen, onOpenChange)
  return (
    <PopupContext value={state}>
      <SelectPrimitive.Root {...props} open={state.open} onOpenChange={state.setOpen} />
    </PopupContext>
  )
}
const SelectGroup = SelectPrimitive.Group
const SelectValue = SelectPrimitive.Value

/**
 * variant="inline"：设置面板、检查器里「标签 … 值 ⌄」那一行右端的值。平放，没有凹槽；悬停和展开时浅灰底（像 macOS 的弹出按钮），
 * 文字落在行的右边线上（edge-end），宽度跟着值走。
 */
function SelectTrigger({
  className,
  children,
  variant = "field",
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Trigger> & { variant?: "field" | "inline" }) {
  return (
    <SelectPrimitive.Trigger
      data-slot="select-trigger"
      data-variant={variant}
      className={cn(
        variant === "field"
          ? [fieldSurface, "flex h-(--ds-h-md) w-full min-w-0 items-center justify-between gap-2 rounded-control px-3 text-left text-sm text-fg outline-none"]
          : [
              // 行末用 edge-end 时推出去的是自己的内边距 8 和 ⌄ 两侧的留白 3，⌄ 的线条落在内容线上
              "inline-flex h-(--ds-h-sm) min-w-0 items-center gap-1 rounded-control px-2 text-sm text-fg outline-none select-none [--ds-edge:--spacing(2)] [--ds-ink-inset:3px]",
              "transition-colors duration-(--ds-dur-fast) ease-ds hover:bg-hover data-[state=open]:bg-hover focus-visible:ring-2 focus-visible:ring-focus/40",
              "[&_svg]:size-4 [&_svg]:shrink-0",
            ],
        "data-placeholder:text-fg-subtle",
        "[&>span]:truncate",
        className
      )}
      {...props}
    >
      {children}
      <SelectPrimitive.Icon asChild>
        <ChevronDown className="size-4 shrink-0 text-fg-subtle" aria-hidden />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  )
}

function SelectContent({
  className,
  children,
  sideOffset = 8,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Content>) {
  const { open } = usePopup()
  // 收起且退场播完后，照样挂一份不可见的内容层（Radix 把它放进 DocumentFragment）：触发器里显示的选中文字是选项的 ItemText
  // 传送过去的，内容层整个卸掉时触发器会变空。退场期间不挂，免得两份 ItemText 同时把文字传进去。
  const [present, setPresent] = React.useState(open)
  if (open && !present) setPresent(true)
  return (
    <>
      {present ? null : (
        <SelectPrimitive.Content position="popper" collisionPadding={8}>
          <SelectPrimitive.Viewport>{children}</SelectPrimitive.Viewport>
        </SelectPrimitive.Content>
      )}
      <AnimatePresence custom={fromKeyboard()} onExitComplete={() => setPresent(false)}>
        {open ? (
          <SelectPrimitive.Portal forceMount>
            <SelectPrimitive.Content forceMount asChild position="popper" sideOffset={sideOffset} collisionPadding={8} {...props}>
              <PopupMotion
                data-slot="select-content"
                from={grow}
                className={cn(
                  dropdownMenuSurface,
                  "z-50 max-h-(--radix-select-content-available-height) min-w-(--radix-select-trigger-width) overflow-y-auto",
                  "origin-(--radix-select-content-transform-origin)",
                  className
                )}
              >
                <SelectPrimitive.Viewport>{children}</SelectPrimitive.Viewport>
              </PopupMotion>
            </SelectPrimitive.Content>
          </SelectPrimitive.Portal>
        ) : null}
      </AnimatePresence>
    </>
  )
}

function SelectItem({ className, children, ...props }: React.ComponentProps<typeof SelectPrimitive.Item>) {
  return (
    <SelectPrimitive.Item
      data-slot="select-item"
      className={cn(dropdownMenuItemClass, "pr-8 data-[state=checked]:font-medium", className)}
      {...props}
    >
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
      <SelectPrimitive.ItemIndicator className="absolute right-3 flex items-center">
        <Check className="text-fg!" aria-hidden />
      </SelectPrimitive.ItemIndicator>
    </SelectPrimitive.Item>
  )
}

function SelectLabel({ className, ...props }: React.ComponentProps<typeof SelectPrimitive.Label>) {
  return <SelectPrimitive.Label className={cn("px-2 pt-2 pb-1 text-xs font-medium text-fg-subtle", className)} {...props} />
}

function SelectSeparator({ className, ...props }: React.ComponentProps<typeof SelectPrimitive.Separator>) {
  return (
    <SelectPrimitive.Separator className={cn("-mx-(--ds-pad-popover) my-1 h-px bg-line", className)} {...props} />
  )
}

export { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectSeparator, SelectTrigger, SelectValue }
