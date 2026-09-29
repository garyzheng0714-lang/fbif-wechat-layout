"use client"

import * as React from "react"
import { motion } from "motion/react"

import { cn } from "@/lib/utils"
import { SPRINGS, STAGGER } from "@/components/ui/ease"

/**
 * 空状态：一句话说明为什么空 + 至多一个下一步操作。不画插图、不套卡片。
 * 图标 20 → 12 → 标题 14 / 500 → 4 → 说明 14 → 16 → 操作。
 *
 * 动效（首次档，DESIGN.md §5.1）：出现时图标、标题、说明、操作依次透明度 0 → 1、上移 4 → 0，
 * 每项错开 50ms，smooth 弹簧（0.35s / bounce 0），总长不超过 400ms。只在挂载时播一次；
 * 文案变化（比如搜索词变了）不重播，免得边打字边闪。减少动态效果时不位移，只淡入。
 */
function Rise({ i, className, children }: { i: number; className?: string; children: React.ReactNode }) {
  return (
    <motion.div
      className={className}
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ ...SPRINGS.smooth, delay: i * STAGGER.item }}
    >
      {children}
    </motion.div>
  )
}

function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: {
  icon?: React.ReactNode
  title: React.ReactNode
  description?: React.ReactNode
  action?: React.ReactNode
  className?: string
}) {
  let i = 0
  return (
    <div data-slot="empty-state" className={cn("grid justify-items-center gap-1 px-6 py-12 text-center text-sm", className)}>
      {icon ? (
        <Rise i={i++} className="mb-2 text-fg-subtle [&_svg]:size-5">
          {icon}
        </Rise>
      ) : null}
      <Rise i={i++} className="font-medium text-fg">
        <p>{title}</p>
      </Rise>
      {description ? (
        <Rise i={i++} className="max-w-72 text-fg-muted">
          <p>{description}</p>
        </Rise>
      ) : null}
      {action ? (
        <Rise i={i++} className="mt-3">
          {action}
        </Rise>
      ) : null}
    </div>
  )
}

export { EmptyState }
