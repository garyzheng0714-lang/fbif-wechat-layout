import { Agentation } from "agentation"

/**
 * 标注工具（Su Design DESIGN.md §8.1）：在页面上点选元素写批注，同步给本机 agentation-mcp（4747）。
 * 开发环境始终开启；线上只在带 ?review 打开过的浏览器里开启（记在本机），?review=0 关闭。
 */
function enabled() {
  if (import.meta.env.DEV) return true
  try {
    const flag = new URLSearchParams(location.search).get("review")
    if (flag !== null) localStorage.setItem("fbif-review", flag === "0" ? "0" : "1")
    return localStorage.getItem("fbif-review") === "1"
  } catch {
    return false
  }
}

export function Review() {
  return enabled() ? <Agentation appName="FBIF 排版" endpoint="http://localhost:4747" /> : null
}
