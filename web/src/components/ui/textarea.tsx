import * as React from "react"

import { cn } from "@/lib/utils"
import { fieldSurface } from "@/components/ui/input"

/** 多行输入：外观同 Input，最小高度 80，只允许纵向拉伸。 */
function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        fieldSurface,
        "block min-h-20 w-full min-w-0 resize-y rounded-control px-3 py-2 text-sm text-fg outline-none placeholder:text-fg-subtle",
        className
      )}
      {...props}
    />
  )
}

export { Textarea }
