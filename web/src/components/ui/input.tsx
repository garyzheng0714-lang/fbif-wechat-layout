import * as React from "react"

import { cn } from "@/lib/utils"

/**
 * Input、Textarea、SearchField、SelectTrigger 共用外观：
 * 凹槽：浅灰底 + 上沿内阴影。悬停加深一档；聚焦或展开时槽底变白、外圈聚焦环；出错描边变红。
 * 动效：聚焦 → 槽底变白、聚焦环从 0 扩到 2px 并淡入，150ms ease-ds（颜色类，走 CSS 过渡）；不浮起、不缩放。
 */
const fieldSurface = [
  "bg-well shadow-inset transition-[background-color,box-shadow] duration-(--ds-dur-fast) ease-ds",
  "hover:bg-active",
  "focus:bg-card focus:ring-2 focus:ring-focus/20 focus-within:bg-card focus-within:ring-2 focus-within:ring-focus/20",
  "data-[state=open]:bg-card data-[state=open]:ring-2 data-[state=open]:ring-focus/20",
  "aria-invalid:shadow-[inset_0_0_0_1px_var(--ds-danger)]",
  "disabled:cursor-not-allowed disabled:opacity-40",
]

/** 表单输入框：用于设置、表单。搜索请用 SearchField。 */
function Input({ className, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      data-slot="input"
      className={cn(
        fieldSurface,
        "h-(--ds-h-md) w-full min-w-0 rounded-control px-3 text-sm text-fg outline-none placeholder:text-fg-subtle",
        className
      )}
      {...props}
    />
  )
}

export { Input, fieldSurface }
