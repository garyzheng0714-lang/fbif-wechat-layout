import { useRef, useState, type ClipboardEvent, type KeyboardEvent } from "react"
import { AtSign, Check, ChevronDown, FileText, Paperclip, X } from "lucide-react"

import { Avatar } from "@/components/ui/avatar"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Progress } from "@/components/ui/progress"
import { Surface } from "@/components/ui/surface"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip } from "@/components/ui/tooltip"

import { ACCOUNTS, CHANNEL_LABEL, type Account, type Channel } from "./accounts"
import type { ImportSource, ImportState } from "./use-import-simulation"

/**
 * 导入卡：结构照现有排版工具。首行「排版到 + 账号菜单」（单层菜单，渠道只分组，红线 2）；
 * 输入框粘贴即排版（红线 32），手输 ⌘/Ctrl+Enter；没有提交按钮，动作行只留上传、飞书云文档和「粘贴后自动排版」；
 * 进度在动作行之后（红线 2）。卡里的字只有两条竖线：动作行开头的按钮用 edge-start，图标线条落在卡片的文字线上。
 */
export function ImportCard({
  account,
  state,
  onPickAccount,
  onImport,
  onClearFile,
}: {
  account: Account
  state: ImportState
  onPickAccount: (a: Account) => void
  onImport: (source: ImportSource) => void
  onClearFile: () => void
}) {
  const [draft, setDraft] = useState("")
  const file = useRef<HTMLInputElement>(null)
  const busy = state.stage === "importing"
  const current = state.stage === "empty" ? null : state.source

  const submit = (text: string) => {
    const value = text.trim()
    if (value) onImport({ kind: "text", label: value })
  }

  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const el = e.currentTarget
    // 等浏览器把粘贴内容写进输入框，再按最终全文提交
    requestAnimationFrame(() => submit(el.value))
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit(draft)
    }
  }

  return (
    <Surface>
      <div className="flex items-center gap-2">
        <span className="text-sm text-fg-muted">排版到</span>
        <AccountMenu account={account} onPick={onPickAccount} />
      </div>

      <div className="grid gap-item">
        <Textarea
          aria-label="导入内容"
          rows={4}
          value={draft}
          spellCheck={false}
          placeholder="粘贴公众号文章 / 飞书文档链接，或正文"
          onChange={(e) => setDraft(e.target.value)}
          onPaste={onPaste}
          onKeyDown={onKeyDown}
        />

        {current?.kind === "file" ? (
          <div className="flex items-center gap-2">
            <FileText className="size-4 shrink-0 text-fg-subtle" aria-hidden />
            <span className="min-w-0 flex-1 truncate text-sm text-fg">{current.label}</span>
            <span className="text-xs text-fg-subtle tabular-nums">{current.size}</span>
            <Tooltip content="移除文件" side="left">
              <Button variant="ghost" size="icon" className="edge-end" aria-label="移除文件" onClick={onClearFile}>
                <X />
              </Button>
            </Tooltip>
          </div>
        ) : null}

        <div className="flex items-center gap-1">
          <Button variant="ghost" size="sm" className="edge-start" onClick={() => file.current?.click()}>
            <Paperclip />
            {current?.kind === "file" ? "重新选择文件" : "上传文件"}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setDraft((d) => (d.startsWith("@") ? d : "@" + d))}>
            <AtSign />
            飞书云文档
          </Button>
          {/* 必要提示：红线 32 规定导入卡没有提交按钮，动作行只留这一句，告诉用户粘贴后不用再点什么 */}
          <span className="ml-auto text-xs text-fg-subtle">粘贴后自动排版</span>
        </div>
      </div>

      {busy ? (
        <div className="grid gap-part">
          <Progress value={state.percent} aria-label="导入总进度" />
          <div className="flex items-center justify-between text-xs text-fg-muted">
            <span>{state.label}</span>
            <span className="tabular-nums">{state.percent}%</span>
          </div>
        </div>
      ) : null}

      <input
        ref={file}
        type="file"
        accept=".docx,.doc,.md,.txt"
        hidden
        onChange={(e) => {
          const picked = e.target.files?.[0]
          if (picked) onImport({ kind: "file", label: picked.name, size: formatSize(picked.size) })
          e.target.value = ""
        }}
      />
    </Surface>
  )
}

function AccountMenu({ account, onPick }: { account: Account; onPick: (a: Account) => void }) {
  const channels: Channel[] = ["mp", "ft"]
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" aria-label={`排版账号：${account.name}`}>
          {/* 账号是组织：圆角方形，不裁成圆（Su Design 头像规则） */}
          <Avatar name={account.name} src={account.avatar} size={16} />
          {account.name}
          <ChevronDown />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        {channels.map((ch, i) => (
          <DropdownMenuGroup key={ch}>
            {i > 0 ? <DropdownMenuSeparator /> : null}
            <DropdownMenuLabel>{CHANNEL_LABEL[ch]}</DropdownMenuLabel>
            {ACCOUNTS.filter((a) => a.channel === ch).map((a) => (
              <DropdownMenuItem key={a.id} onSelect={() => onPick(a)}>
                <Avatar name={a.name} src={a.avatar} size={16} />
                <span className="flex-1">{a.name}</span>
                {a.id === account.id ? <Check aria-label="当前账号" /> : null}
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function formatSize(bytes: number) {
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`
}
