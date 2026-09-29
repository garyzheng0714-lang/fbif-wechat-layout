"use client"

import { useState } from "react"

import { cn } from "@/lib/utils"

/**
 * 头像：有图用图，无图或加载失败时显示 1–2 字缩写。人用圆形，组织和来源用圆角方形。
 * 尺寸 16 / 24 / 32 / 40；缩写字号 13 / 13 / 15 / 17。16 放不下缩写，只给图片或组织标志。color 只用于来源或组织的品牌色。
 *
 * 动效：图片加载完成 → 图片透明度 0 → 1、缩写（连同底色）1 → 0，同时 150ms ease 交叉替换，不用弹簧、不缩放。
 * 两层叠在同一格里，图片没到之前先显示缩写，头像不闪空白、不跳尺寸；已在缓存里的图片第一帧就到位，不播淡入。
 * 加载失败时图片层撤掉，缩写留在原处。减少动态效果时保留这次淡入淡出（只有透明度）。
 */
const SIZE = {
  16: "size-4 text-xs",
  24: "size-6 text-xs",
  32: "size-8 text-sm",
  40: "size-10 text-base",
} as const

function Avatar({
  name,
  initials,
  color,
  src,
  size = 24,
  shape = "rounded",
  className,
}: {
  name: string
  initials?: string
  color?: string
  src?: string
  size?: keyof typeof SIZE
  shape?: "rounded" | "circle"
  className?: string
}) {
  const [failed, setFailed] = useState<string | null>(null)
  const [loaded, setLoaded] = useState<string | null>(null)
  const hasImage = Boolean(src) && failed !== src
  const ready = hasImage && loaded === src
  const label = initials ?? name.slice(0, 1)
  return (
    <span
      role="img"
      aria-label={name}
      data-slot="avatar"
      data-state={ready ? "image" : "fallback"}
      className={cn(
        // 垫一层实底：缩写底色 fg-subtle 是半透明的，叠放时重叠处会发黑
        "inline-grid shrink-0 overflow-hidden bg-canvas leading-none font-medium text-white select-none *:[grid-area:1/1]",
        SIZE[size],
        shape === "circle" ? "rounded-full" : size >= 32 ? "rounded-control" : "rounded-sm",
        className
      )}
    >
      <span
        data-slot="avatar-fallback"
        aria-hidden
        className={cn(
          "grid place-items-center transition-opacity duration-(--ds-dur-fast) ease-[ease]",
          !color && "bg-fg-subtle",
          ready && "opacity-0"
        )}
        style={color ? { background: color } : undefined}
      >
        {label}
      </span>
      {hasImage ? (
        <img
          key={src}
          src={src}
          alt=""
          data-slot="avatar-image"
          className={cn(
            "size-full object-cover transition-opacity duration-(--ds-dur-fast) ease-[ease]",
            !ready && "opacity-0"
          )}
          // 缓存命中时 onLoad 可能在挂载前就触发过：挂载时看一眼 complete，第一帧就到位
          ref={(img) => {
            if (img?.complete && img.naturalWidth > 0 && loaded !== src) setLoaded(src ?? null)
          }}
          onLoad={() => setLoaded(src ?? null)}
          onError={() => setFailed(src ?? null)}
        />
      ) : null}
    </span>
  )
}

export { Avatar }
