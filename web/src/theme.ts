/**
 * 深浅色：地址栏 ?theme=dark|light 优先（截图、验收用），否则跟随系统。
 * 设计变量在 <html data-theme> 上切换（Su Design ds-tokens.css）。
 */
export function applyTheme() {
  const root = document.documentElement
  const forced = new URLSearchParams(location.search).get("theme")
  if (forced === "dark" || forced === "light") {
    root.dataset.theme = forced
    return
  }
  const mq = matchMedia("(prefers-color-scheme: dark)")
  const sync = () => (root.dataset.theme = mq.matches ? "dark" : "light")
  sync()
  mq.addEventListener("change", sync)
}
