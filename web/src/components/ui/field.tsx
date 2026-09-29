"use client"

import * as React from "react"
import { CircleAlert } from "lucide-react"
import { motion, useReducedMotion } from "motion/react"
import { Label as LabelPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { DUR, EASE_OUT, SPRINGS } from "@/components/ui/ease"

/**
 * 表单项：标签 → 控件 → 说明或错误，间距 8。出错时控件用 aria-invalid 显示红边，只有图标为红色。
 *
 * 动效（docs/研究/动效-苹果.md §5）：
 * - 出现错误 → 错误行高度从 0 撑开、淡入（smooth）；下面的内容跟着平稳下移，不跳。
 * - 清除错误 → 原路收起、淡出，快一档（snappy）；收起途中文字保持上一条，不闪空。
 * - 页面一打开就有的错误直接显示，不做入场；不抖输入框。
 * - 减少动态效果：高度直接到位，只淡入淡出 150ms。
 */
function Field({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="field" className={cn("group/field grid gap-2", className)} {...props} />
}

function FieldLabel({ className, ...props }: React.ComponentProps<typeof LabelPrimitive.Root>) {
  return (
    <LabelPrimitive.Root
      data-slot="field-label"
      className={cn(
        "justify-self-start text-sm font-medium text-fg select-none",
        "group-has-[:disabled]/field:cursor-not-allowed group-has-[:disabled]/field:opacity-40",
        className
      )}
      {...props}
    />
  )
}

function FieldDescription({ className, ...props }: React.ComponentProps<"p">) {
  return <p data-slot="field-description" className={cn("text-xs text-fg-subtle", className)} {...props} />
}

function FieldError({ className, children, ...props }: React.ComponentProps<"p">) {
  const reduce = useReducedMotion()
  const shown = Boolean(children)
  // 收起途中还显示上一条错误；收完再清掉
  const [last, setLast] = React.useState(children)
  if (shown && last !== children) setLast(children)
  const text = shown ? children : last
  const fade = { duration: DUR.fast, ease: EASE_OUT }
  return (
    // -mt-2 抵掉 Field 的行间距，里面再用 pt-2 补回：错误不在时这一行真正是 0 高，不留空隙
    <motion.div
      data-slot="field-error"
      className="-mt-2 overflow-hidden"
      initial={false}
      animate={{ height: shown ? "auto" : 0, opacity: shown ? 1 : 0 }}
      transition={
        reduce
          ? { height: { duration: 0 }, opacity: fade }
          : shown
            ? SPRINGS.smooth
            : { height: SPRINGS.snappy, opacity: fade }
      }
      onAnimationComplete={() => {
        if (!shown) setLast(null)
      }}
      aria-hidden={!shown}
    >
      {text ? (
        <p role="alert" className={cn("flex items-start gap-1 pt-2 text-xs text-fg-muted", className)} {...props}>
          <CircleAlert className="mt-px size-4 shrink-0 text-danger" aria-hidden />
          <span>{text}</span>
        </p>
      ) : null}
    </motion.div>
  )
}

export { Field, FieldDescription, FieldError, FieldLabel }
