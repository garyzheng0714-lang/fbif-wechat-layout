import { useEffect, useState, type DragEvent } from "react"
import { UserRound } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { NavItem } from "@/components/ui/nav-item"
import { Topbar } from "@/components/ui/page"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar"
import { Split, SplitPane } from "@/components/ui/split"

import { ACCOUNTS, type Account } from "./accounts"
import { ArticlePreview, PreviewEmpty } from "./article-preview"
import { BlockPanelCard } from "./block-panel-card"
import { ImportCard } from "./import-card"
import { useImportSimulation, type ImportSource } from "./use-import-simulation"

/**
 * 编辑器原型：结构照现有排版工具（用户 2026-09-29：保持之前的样式），外框和细节照 Su Design。
 * - 整窗 SidebarProvider + Sidebar + SidebarInset；侧栏是放表单卡片的工具栏，宽 400（width={400}）。
 * - 侧栏（灰底）：导入卡 → 底部区域卡；最底一行是登录身份（菜单向上开），同一行右端是收起按钮（用户 2026-09-29：放第一栏右下角），
 *   指到侧栏上才出现；收起后预览区顶栏最左边是展开按钮，⌘\ 开合、碰左边缘浮出都由 Sidebar 自带。
 * - 预览区是一张白卡（Split 默认 gap：离窗口上右下各 8），页面动作只在它的顶栏：右上「复制全文」。
 */
export function EditorPrototype() {
  const [accountId, setAccountId] = useState("fbif")
  const account = ACCOUNTS.find((a) => a.id === accountId) ?? ACCOUNTS[0]
  const { state, start, reset } = useImportSimulation()
  const ready = state.stage === "ready"

  const pickAccount = (next: Account) => {
    // 跨渠道（跨模板）切换在正式实现里是整页跳转，原型里清空当前稿
    if (next.channel !== account.channel) reset()
    setAccountId(next.id)
  }

  // 导入后在输入框以外的地方粘贴，也按新内容整稿重排（红线 32）
  useEffect(() => {
    const onPaste = (e: ClipboardEvent) => {
      const target = e.target as HTMLElement | null
      if (target?.closest("input, textarea, [contenteditable]")) return
      const text = e.clipboardData?.getData("text/plain").trim()
      if (text) start({ kind: "text", label: text })
    }
    window.addEventListener("paste", onPaste)
    return () => window.removeEventListener("paste", onPaste)
  }, [start])

  const onDrop = (e: DragEvent) => {
    e.preventDefault()
    const file = e.dataTransfer.files?.[0]
    if (file) start(fileSource(file))
  }

  return (
    // 整窗都能拖入文件（包括侧栏）：contents 不生成盒子，只接住冒泡上来的拖放事件
    <div className="contents" onDragOver={(e) => e.preventDefault()} onDrop={onDrop}>
    <SidebarProvider width={400}>
      <Sidebar label="导入与设置">
        <SidebarContent>
          <ImportCard account={account} state={state} onPickAccount={pickAccount} onImport={start} onClearFile={reset} />
          {ready ? <BlockPanelCard /> : null}
        </SidebarContent>
        {/* 左 8：身份行的悬停底色和上面卡片的左边对齐；右 20：收起按钮用 edge-end 推出去后悬停底色离边 9，和左边对称 */}
        <SidebarFooter className="flex items-center gap-1 pr-5">
          <Identity />
          <SidebarTrigger className="edge-end opacity-0 transition-opacity duration-(--ds-dur-fast) group-hover/sidebar:opacity-100 focus-visible:opacity-100" />
        </SidebarFooter>
      </Sidebar>

      <SidebarInset className="h-svh">
        <Split>
          {/* 预览模拟公众号白底正文：深色模式下这张卡也保持浅色（红线 1 预览全白） */}
          <SplitPane data-theme="light" aria-label="预览区">
            <PreviewTopbar ready={ready} />
            {ready ? <ArticlePreview account={account} /> : <PreviewEmpty />}
          </SplitPane>
        </Split>
      </SidebarInset>
    </SidebarProvider>
    </div>
  )
}

/** 顶栏在白卡顶上：左边只在侧栏收起（或窄屏）时放展开按钮；右边唯一的页面动作「复制全文」嵌进卡片右上角（pr-1）。 */
function PreviewTopbar({ ready }: { ready: boolean }) {
  const { open, desktop } = useSidebar()
  return (
    <Topbar className="pr-1">
      {desktop && open ? null : <SidebarTrigger className="edge-start" />}
      <Button variant="primary" className="ml-auto" disabled={!ready} onClick={() => toast("已复制到剪贴板")}>
        复制全文
      </Button>
    </Topbar>
  )
}

/** 侧栏最底下一行：登录身份，和 Su Design 侧栏底部的账号入口同一个样子（一行导航项）；菜单向上开（红线 2）。 */
function Identity() {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <NavItem icon={<UserRound aria-hidden />} className="min-w-0 flex-1">
          本地验证
        </NavItem>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start">
        <DropdownMenuLabel>账号登录 · 云文档不可用</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem>规则后台</DropdownMenuItem>
        <DropdownMenuItem>退出登录</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function fileSource(file: File): ImportSource {
  const mb = file.size / 1024 / 1024
  return { kind: "file", label: file.name, size: mb >= 1 ? `${mb.toFixed(1)} MB` : `${Math.max(1, Math.round(file.size / 1024))} KB` }
}
