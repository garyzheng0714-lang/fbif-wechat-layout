import * as React from "react"

/**
 * 键盘层（DESIGN.md §4.2）：应用里的快捷键都从这里挂，不在组件里各写一个 keydown。
 *
 * - 输入法组字时（isComposing，或 keyCode 229）一律不处理：中文、日文输入时按 J 是在打字，不是换文章（dd-01）。
 * - 焦点在输入框、文本域、下拉、可编辑区域里时，不带修饰键的单键不触发；带 ⌘/Ctrl/⌥ 的组合键照常。
 * - 监听挂在 ownerDocument.defaultView 上，不直接写 window：组件被渲染进 iframe 或 Portal 时也能收到（dd-02）。
 * - scope：只在焦点落在这块区域里时生效，同一页面上的多个应用互不抢键。
 * - 触发即执行，不做任何动画；键盘动作的界面变化一律 0ms（DESIGN.md §5.1）。
 *
 * 写法："j"、"Escape"、"mod+k"（mod 在 macOS 是 ⌘，其他系统是 Ctrl）、"alt+1"、"shift+?"。
 * 数字键按物理键位匹配（Digit1），⌥ 在 macOS 上把 1 变成 ¡ 也照样认。
 */

/**
 * 最近一次输入是不是键盘（全页共用一个监听）：按下任意键记为键盘；按下或真正移动指针记为指针。
 * 组件据此决定「这次换项 / 开合要不要动画」——键盘触发的高频动作一律 0ms（DESIGN.md §5.1）。
 */
let keyboard = false
if (typeof document !== "undefined") {
  document.addEventListener("keydown", () => (keyboard = true), true)
  document.addEventListener("pointerdown", () => (keyboard = false), true)
  document.addEventListener("pointermove", (e) => {
    if (e.movementX || e.movementY) keyboard = false
  }, true)
}
export const fromKeyboard = () => keyboard

type HotkeyHandler = (event: KeyboardEvent) => void
type HotkeyMap = Record<string, HotkeyHandler>
type HotkeyOptions = {
  /** 只在焦点位于这个元素之内时生效；不传则整页生效 */
  scope?: React.RefObject<HTMLElement | null>
  enabled?: boolean
}
type Modifier = "alt" | "mod" | "shift"

const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform)

/** 焦点在可以打字的地方：这时单键属于输入，不属于快捷键。 */
function isEditable(target: EventTarget | null) {
  if (!(target instanceof Element)) return false
  if (target.closest("input, textarea, select, [contenteditable=''], [contenteditable=true]")) return true
  return target.closest("[role=textbox], [role=combobox], [role=searchbox]") !== null
}

function isComposing(event: KeyboardEvent) {
  return event.isComposing || event.keyCode === 229
}

function matches(spec: string, event: KeyboardEvent) {
  const parts = spec.toLowerCase().split("+")
  const key = parts.pop() ?? ""
  const want = { mod: parts.includes("mod"), alt: parts.includes("alt"), shift: parts.includes("shift") }
  const mod = IS_MAC ? event.metaKey : event.ctrlKey
  if (mod !== want.mod || event.altKey !== want.alt) return false
  // "?" 这类本身要按 shift 才打得出的键，不强求写 shift
  if (want.shift && !event.shiftKey) return false
  if (/^[0-9]$/.test(key)) return event.code === `Digit${key}`
  return event.key.toLowerCase() === key
}

function viewOf(scope?: React.RefObject<HTMLElement | null>) {
  return scope?.current?.ownerDocument.defaultView ?? (typeof window === "undefined" ? null : window)
}

/** 挂一组快捷键。回调总是拿到最新的闭包，不需要 useCallback。 */
function useHotkeys(map: HotkeyMap, { scope, enabled = true }: HotkeyOptions = {}) {
  const latest = React.useRef(map)
  React.useLayoutEffect(() => {
    latest.current = map
  })
  React.useEffect(() => {
    const view = viewOf(scope)
    if (!enabled || !view) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || isComposing(event)) return
      if (scope && !scope.current?.contains(event.target as Node)) return
      const modified = event.metaKey || event.ctrlKey || event.altKey
      if (!modified && isEditable(event.target)) return
      for (const [spec, handler] of Object.entries(latest.current)) {
        if (!matches(spec, event)) continue
        event.preventDefault()
        handler(event)
        return
      }
    }
    view.addEventListener("keydown", onKeyDown)
    return () => view.removeEventListener("keydown", onKeyDown)
  }, [scope, enabled])
}

/**
 * 按住修饰键超过 delay 毫秒返回 true（dd-01《按住 ⌘ 显示快捷键提示》）：用来在行尾槽位显示键位，松手即收。
 * 期间按了别的非数字键（说明在用别的组合键）、或窗口失焦，立即收起。
 */
function useModifierHold(modifier: Modifier, { scope, delay = 300 }: { scope?: React.RefObject<HTMLElement | null>; delay?: number } = {}) {
  const [held, setHeld] = React.useState(false)
  React.useEffect(() => {
    const view = viewOf(scope)
    if (!view) return
    const name = { alt: "Alt", mod: IS_MAC ? "Meta" : "Control", shift: "Shift" }[modifier]
    let timer = 0
    const reset = () => {
      view.clearTimeout(timer)
      timer = 0
      setHeld(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === name) {
        if (event.repeat || timer) return
        if (scope && !scope.current?.contains(event.target as Node)) return
        timer = view.setTimeout(() => setHeld(true), delay)
      } else if (!event.code.startsWith("Digit")) reset()
    }
    const onKeyUp = (event: KeyboardEvent) => {
      if (event.key === name) reset()
    }
    view.addEventListener("keydown", onKeyDown)
    view.addEventListener("keyup", onKeyUp)
    view.addEventListener("blur", reset)
    return () => {
      view.clearTimeout(timer)
      view.removeEventListener("keydown", onKeyDown)
      view.removeEventListener("keyup", onKeyUp)
      view.removeEventListener("blur", reset)
    }
  }, [modifier, scope, delay])
  return held
}

/** 键位的显示写法：macOS 用符号 ⌘ ⌥ ⇧，其他系统用 Ctrl Alt Shift。 */
function formatHotkey(spec: string) {
  const names: Record<string, string> = IS_MAC
    ? { mod: "⌘", alt: "⌥", shift: "⇧" }
    : { mod: "Ctrl+", alt: "Alt+", shift: "Shift+" }
  return spec
    .split("+")
    .map((p) => names[p.toLowerCase()] ?? (p.length === 1 ? p.toUpperCase() : p))
    .join("")
}

export { formatHotkey, isComposing, isEditable, useHotkeys, useModifierHold }
export type { HotkeyMap, HotkeyOptions }
