export const EASE_OUT = [0.23, 1, 0.32, 1] as const
export const EASE_MOVE = [0.77, 0, 0.175, 1] as const
export const EASE_PANEL = [0.32, 0.72, 0, 1] as const
export const EASE_SOFT = [0.25, 0.46, 0.45, 0.94] as const
export const EASE_MEDIA = [0.87, 0, 0.13, 1] as const
export const EASE_EXPO = [0.19, 1, 0.22, 1] as const

/**
 * 秒。press 按下 · fast 悬停、tooltip、轨道滑块 · base 菜单、对话框 · slow 面板、侧栏。离开 = 进入低一档。
 * enter 区块入场 · media 截图入场 · hero 首屏主视觉（标题 1.0、字标 1.2、窗口 1.4、底座 1.6 依次拉长）。
 */
export const DUR = { press: 0.1, fast: 0.15, base: 0.2, slow: 0.3, enter: 0.4, media: 0.7, hero: 1.2 } as const

/** 错落：同一组依次出现的间隔（秒）。区块里的子项 50ms；逐字 18ms。 */
export const STAGGER = { item: 0.05, char: 0.018 } as const

/**
 * 不过冲的弹簧（bounce 0），已编译成 CSS linear()，可以直接写进 transition-timing-function，不用 JS。
 * 由 motion 按 bounce 0 生成的原串：
 * - settle：卡片、列表项落位，450ms。
 * - drift：悬停时贴纸、缩略图轻移，进 350ms。
 */
export const SPRING = {
  settle:
    "linear(0, 0.0192, 0.0669, 0.1317, 0.2054, 0.2824, 0.3586, 0.4317, 0.5001, 0.563, 0.62, 0.6711, 0.7164, 0.7564, 0.7914, 0.8219, 0.8483, 0.8711, 0.8907, 0.9075, 0.9218, 0.9341, 0.9445, 0.9533, 0.9608, 0.9671, 0.9724, 0.9769, 0.9807, 0.9839, 0.9865, 0.9888, 0.9906, 0.9922, 0.9935, 0.9946, 0.9955, 0.9963, 0.9969, 0.9974, 0.9979, 0.9982, 0.9985, 0.9988, 1)",
  drift:
    "linear(0, 0.0363, 0.1199, 0.2243, 0.3331, 0.4372, 0.5318, 0.615, 0.6864, 0.7464, 0.7964, 0.8374, 0.8708, 0.8977, 0.9194, 0.9366, 0.9504, 0.9612, 0.9698, 0.9765, 0.9817, 0.9859, 0.9891, 0.9915, 0.9935, 0.995, 0.9961, 0.997, 0.9977, 0.9983, 0.9987, 0.999, 1, 1, 1)",
} as const

/** 滚动、鼠标联动的值统一过一层这个弹簧再用（useSpring 100/30），手感跟手但不抖。 */
export const FOLLOW = { stiffness: 100, damping: 30 } as const

/**
 * 由「看起来多久（秒）+ 回弹多少」换算成物理弹簧（docs/研究/动效-苹果.md §1.4）。
 * 必须写成 stiffness / damping：motion ≥ 12.34 对 visualDuration / bounce 写法会在打断时把继承的速度清零，
 * 物理写法才能中途换目标时从当前位置、当前速度接着走。
 */
export const spring = (visualDuration: number, bounce = 0) => {
  const stiffness = ((2 * Math.PI) / (1.2 * visualDuration)) ** 2
  return { type: "spring", stiffness, damping: 2 * (1 - bounce) * Math.sqrt(stiffness), mass: 1 } as const
}

/**
 * 弹簧预设（DESIGN.md §5.2）：和苹果 SwiftUI 的 duration / bounce 同一套思路，过冲上限 bounce 0.25。
 * - snappy（0.22 / 0.15，峰值过冲 0.6%）：控件换位的领先边、菜单弹出、按下回弹、图标变形、数字滚动。
 * - smooth（0.35 / 0）：尾边、面板、对话框、折叠、布局；位移超过 200px 只用它。
 * - bouncy（0.40 / 0.25，过冲 2.8%）：只给确认、松手弹回、轻提示落定。
 * - follow（0.125 / 0.14）：跟手的值（苹果 interactiveSpring）。
 * - lead / trail：前后沿弹簧（stretch.ts）——前沿 = snappy 先到，后沿 = smooth 跟上，差值就是途中的拉伸（参考视频实测拉长约 40%）。
 * 键盘触发的高频操作一律 0ms。
 */
export const SPRINGS = {
  snappy: spring(0.22, 0.15),
  smooth: spring(0.35),
  bouncy: spring(0.4, 0.25),
  follow: spring(0.125, 0.14),
  lead: spring(0.22, 0.15),
  trail: spring(0.35),
} as const
