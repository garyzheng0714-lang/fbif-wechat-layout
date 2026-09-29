import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(__dirname, "src") } },
  // 预览纸沿用正文契约的 public/css/wx-theme.css（渲染器与复制链路的真源），不在 web/ 里复制一份。
  server: { port: 5420, strictPort: true, fs: { allow: [path.resolve(__dirname, ".."), path.resolve(__dirname)] } },
})
