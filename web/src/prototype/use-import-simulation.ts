import { useCallback, useEffect, useRef, useState } from "react"

/**
 * 原型用的导入过程：只模拟阶段与进度，看布局和状态切换。
 * 正式实现接 engine.js 的真实进度（飞书 export job 的 done/total、图片镜像数），这里不接后端。
 * latest-wins：新的一次导入会作废正在走的旧导入（红线 25 的行为，在原型里也保持）。
 */
export type ImportSource = { kind: "file"; label: string; size: string } | { kind: "text"; label: string }

export type ImportState =
  | { stage: "empty" }
  | { stage: "importing"; label: string; percent: number; source: ImportSource }
  | { stage: "ready"; source: ImportSource }

const PHASES = [
  { until: 30, label: "正在读取文档" },
  { until: 85, label: "正在准备正文图片" },
  { until: 100, label: "正在排版" },
]

export function useImportSimulation() {
  const [state, setState] = useState<ImportState>({ stage: "empty" })
  const run = useRef(0)

  useEffect(() => () => void (run.current += 1), [])

  const start = useCallback((source: ImportSource) => {
    const id = ++run.current
    let percent = 0
    const tick = () => {
      if (run.current !== id) return
      percent = Math.min(100, percent + 7)
      if (percent >= 100) {
        setState({ stage: "ready", source })
        return
      }
      const phase = PHASES.find((p) => percent <= p.until) ?? PHASES[PHASES.length - 1]
      setState({ stage: "importing", label: phase.label, percent, source })
      window.setTimeout(tick, 90)
    }
    tick()
  }, [])

  const reset = useCallback(() => {
    run.current += 1
    setState({ stage: "empty" })
  }, [])

  return { state, start, reset }
}
