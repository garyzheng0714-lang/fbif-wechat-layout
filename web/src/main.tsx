import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { MotionConfig } from "motion/react"

import "./index.css"
import { SPRINGS } from "@/components/ui/ease"
import { Toaster } from "@/components/ui/toaster"
import { TooltipProvider } from "@/components/ui/tooltip"

import { EditorPrototype } from "./prototype/editor-prototype"
import { Review } from "./review"
import { applyTheme } from "./theme"

applyTheme()

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MotionConfig reducedMotion="user" transition={SPRINGS.smooth}>
      <TooltipProvider>
        <EditorPrototype />
        <Toaster />
        <Review />
      </TooltipProvider>
    </MotionConfig>
  </StrictMode>
)
