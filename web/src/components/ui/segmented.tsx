"use client"

import * as React from "react"
import { ToggleGroup as ToggleGroupPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"

import { LiftedCard } from "@/components/ui/lifted-card"

/**
 * 分段切换：同一维度 2–4 个互斥视图，如「全部 / 进行中」。
 * 轨道高 28、圆角 8、内边距 2，滑块白底凸起、圆角 6（同心）。图标 16，默认 fg-subtle，当前 fg。
 * 动效照 iOS 的分段控件（滑块是 LiftedCard raised，细节见那里）：
 * - 点击 → 滑块两条边弹簧，前沿 snappy 先到、后沿 smooth 跟上，途中拉长约 40%；
 * - 按住当前项 → 滑块 scale .96；按住左右拖 → 滑块跟手（follow），松手吸到最近一段并切换；
 * - 键盘（方向键 + 空格）→ 直接到位；减少动态效果 → 直接到位。
 */
function Segmented({
  className,
  value,
  onValueChange,
  items,
  group,
  "aria-label": ariaLabel,
}: {
  className?: string
  value: string
  onValueChange: (value: string) => void
  items: { value: string; label: React.ReactNode; ariaLabel?: string; disabled?: boolean }[]
  /** 滑块的名字（写在 data-group 上），页面内唯一 */
  group: string
  "aria-label": string
}) {
  return (
    <ToggleGroupPrimitive.Root
      data-slot="segmented"
      type="single"
      value={value}
      onValueChange={(next) => next && onValueChange(next)}
      aria-label={ariaLabel}
      className={cn(
        "relative inline-flex h-(--ds-h-md) touch-pan-y items-center rounded-track bg-well p-(--ds-pad-track) shadow-inset",
        className
      )}
    >
      <LiftedCard
        group={group}
        variant="raised"
        className="rounded-track-item"
        onPick={(el) => {
          const next = el.dataset.value
          if (next && next !== value) onValueChange(next)
        }}
      />
      {items.map((item) => (
        <ToggleGroupPrimitive.Item
          key={item.value}
          value={item.value}
          data-slot="segmented-item"
          data-value={item.value}
          aria-label={item.ariaLabel}
          disabled={item.disabled}
          className={cn(
            "relative inline-flex h-full items-center justify-center rounded-track-item px-3 text-sm font-medium whitespace-nowrap text-fg-muted outline-none select-none",
            "hover:text-fg",
            "focus-visible:ring-2 focus-visible:ring-focus/40",
            "disabled:pointer-events-none disabled:opacity-40",
            "data-[state=on]:text-fg",
            "[&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-fg-subtle",
            "hover:[&_svg]:text-fg data-[state=on]:[&_svg]:text-fg"
          )}
        >
          <span className="relative z-10 inline-flex items-center justify-center gap-1.5">{item.label}</span>
        </ToggleGroupPrimitive.Item>
      ))}
    </ToggleGroupPrimitive.Root>
  )
}

export { Segmented }
