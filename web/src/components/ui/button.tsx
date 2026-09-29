import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { useComposedRefs } from "motion/react"
import { Slot } from "radix-ui"

import { cn } from "@/lib/utils"
import { Keys } from "@/components/ui/kbd"
import { usePressScale } from "@/components/ui/stretch"

/**
 * 按钮：4 种变体 × 4 种尺寸。一屏最多一个 primary；工具类操作用 ghost。
 * 质感：primary 是墨色实心加上沿亮线、secondary 灰底加发丝线、字重 500；数值全在 --base-*，组件不写死。
 * shortcut：按钮末尾半透明的按键提示（如对话框确认按钮的 ↵，画成 lucide 图标），告诉用户可以直接按键。
 * 作为菜单、弹出层、选择器的触发器时，打开期间保持按下的样子（Radix 给弹出类触发器的 aria-haspopup + data-state=open；折叠区的触发器不算）：手指还没离开那件东西。
 *
 * 动效（docs/研究/动效-苹果.md §5）：
 * - 指针按下 → 整个按钮缩到 0.97，100ms ease-out 立刻压下（不用弹簧：手指落下那一刻就要有反应）；阴影同时压平。
 * - 松手或指针移开 → snappy 弹回 1，从当前缩放和速度接着走，按下途中松手也不跳。
 * - 键盘（Enter / 空格）不缩放；减少动态效果时缩放直接到位，颜色照常过渡。
 * 缩放由 motion 逐帧写到元素的 transform 上，所以 asChild 渲染成链接也一样。
 */
const PRESSED = 0.97

const buttonVariants = cva(
  [
    "inline-flex shrink-0 items-center justify-center gap-1.5 [font-weight:var(--ds-weight-control)] whitespace-nowrap select-none",
    "transition-[background-color,color,box-shadow] duration-(--ds-dur-fast) ease-ds",
    "outline-none focus-visible:ring-2 focus-visible:ring-focus/40 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas",
    "disabled:pointer-events-none disabled:opacity-40",
    "[&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0",
  ],
  {
    variants: {
      variant: {
        primary: "bg-primary text-primary-fg shadow-solid hover:bg-primary-hover active:shadow-solid-pressed",
        secondary: "bg-fill text-fg shadow-secondary hover:bg-fill-hover active:shadow-pressed [&[aria-haspopup][data-state=open]]:bg-fill-hover [&[aria-haspopup][data-state=open]]:shadow-pressed",
        ghost: "text-fg-muted hover:bg-hover hover:text-fg aria-pressed:bg-active aria-pressed:text-fg [&[aria-haspopup][data-state=open]]:bg-hover [&[aria-haspopup][data-state=open]]:text-fg",
        danger: "bg-danger text-white shadow-solid hover:brightness-95 active:shadow-solid-pressed",
      },
      size: {
        sm: "h-(--ds-h-sm) rounded-sm px-(--ds-pad-x-sm) text-xs [--ds-edge:var(--ds-pad-x-sm)]",
        md: "h-(--ds-h-md) rounded-control px-(--ds-pad-x-md) text-sm [--ds-edge:var(--ds-pad-x-md)]",
        lg: "h-(--ds-h-lg) rounded-control px-(--ds-pad-x-lg) text-base [--ds-edge:var(--ds-pad-x-lg)]",
        icon: "size-(--ds-h-md) rounded-control",
      },
    },
    defaultVariants: { variant: "secondary", size: "md" },
  }
)

function Button({
  className,
  variant,
  size,
  asChild = false,
  shortcut,
  children,
  ref,
  onPointerDown,
  onPointerUp,
  onPointerLeave,
  onPointerCancel,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & { asChild?: boolean; shortcut?: string }) {
  const Comp = asChild ? Slot.Root : "button"
  const { node, press, release } = usePressScale<HTMLButtonElement>(PRESSED)
  const refs = useComposedRefs(node, ref)
  return (
    <Comp
      data-slot="button"
      ref={refs}
      className={cn(buttonVariants({ variant, size }), className)}
      onPointerDown={(e) => {
        onPointerDown?.(e)
        press(e)
      }}
      onPointerUp={(e) => {
        onPointerUp?.(e)
        release()
      }}
      onPointerLeave={(e) => {
        onPointerLeave?.(e)
        release()
      }}
      onPointerCancel={(e) => {
        onPointerCancel?.(e)
        release()
      }}
      {...props}
    >
      {asChild ? (
        children
      ) : (
        <>
          {children}
          {shortcut ? (
            <kbd aria-hidden className="ml-0.5 font-sans opacity-60">
              <Keys keys={shortcut} />
            </kbd>
          ) : null}
        </>
      )}
    </Comp>
  )
}

export { Button, buttonVariants }
