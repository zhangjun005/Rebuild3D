// @kron:intent product/illustration-pointcloud/stylization-modes
/**
 * 渲染参数 —— 与 docs/rendering-styles.md §6 的参数清单对齐。
 *
 * 命名约定：TS 侧用 camelCase，uniform 侧用 uPascalCase，两者在 Viewer#applyParams 里一一对应。
 */

export interface RenderParams {
  // ── 输出模式（第一层：要不要立体）──
  /**
   * 'image' = 纯图像风格化：原图当纹理，复用半调/排线/点画合成 pass，**不碰深度**。
   * 线稿/五官/装饰完整保留，永远不难看，全部在浏览器内完成。
   * 'cloud' = 点云模式：有视差/立体感，但受深度分辨率限制（线稿在深度里不存在）。
   */
  renderMode: 'image' | 'cloud';

  // ── 墨量映射（两种模式共用）──
  /**
   * 墨量来源：0 = 按亮度（传统，适合深色主体）；1 = **按局部结构（默认）**，局部梯度大才上墨，
   * 平坦区留纸 → 修「浅色主体消失、背景抢戏」；2 = 按深度（强调层次，点云模式有效）。
   */
  inkSource: number;
  /** 主体极性：0 = 自动（按图像亮度分布判断），1 = 亮主体，2 = 暗主体 */
  inkPolarity: number;
  /** 结构增益：按局部结构时的灵敏度（越大越容易在弱纹理上上墨） */
  inkGain: number;

  // ── 观察方式 ──
  /**
   * 相机 FOV（度）。**越小越接近正交投影**。
   *
   * 为什么它是渲染参数而不是生成参数：DA3 输出相对深度、没有内参，FOV 与模型无关，
   * 纯粹是「怎么看这个点云」。42° = 默认透视；1~4° 视觉上等价于正交相机。
   * 透视会让远层在画面上再缩小一次，整幅点云呈以视点为中心的放射状（扇形），
   * 所以插画应该用小 FOV。
   */
  cameraFov: number;

  // ── L1 基元层 ──
  /** 点大小：参考深度处的屏幕像素直径（图像模式下隐藏） */
  pointSize: number;
  /** 软边：0 = 硬边圆点，1 = 最软 */
  softness: number;
  /** 点密度（点数比例）：逐点抽稀，空间均匀 */
  density: number;

  // ── L2 风格层 ──
  /**
   * 保留原色（彩色点画）：1 = 用每点自带的颜色渲染、点密度不再由亮度决定，
   * 印刷质感（纸纹/网点/posterize）改成合成 pass 的叠加层。
   * 0 = 旧的单色墨量路径（亮度 → 点密度），铜版画/半调仍走这条。
   *
   * 为什么默认开：点云里本来就有线稿（在每点颜色里），
   * "亮度→点密度 + 单色" 会把亮主体（白裙）直接抹掉。
   */
  colorMode: number;
  /** 彩色点画的色调分离级数（0/1 = 不分离，4~6 = 像印刷品） */
  colorPosterize: number;
  /** 彩色点画的屏幕空间网点叠加强度 0..1（不参与点生成） */
  colorHalftone: number;
  /**
   * 彩色点画的饱和度增益（0.5~2.0，默认 1.2）：把色度围绕亮度放大，不动明暗。
   * 点云渲染整体比原图淡（点缝露纸 + 色调分离），这是对症的干净手段。
   */
  colorSaturation: number;
  /** 点画强度 0..1（图像模式 = 屏幕空间 stipple 的墨量上限） */
  stipple: number;
  /** 图像模式的点画网点尺度（像素，2~10）：blue noise 打连续图像，不会摩尔纹 */
  stippleScale: number;
  /** 明暗 → 密度曲线 */
  densityGamma: number;
  /** 描边强度 0..2 */
  edgeStrength: number;
  /** 描边阈值（归一化深度梯度） */
  edgeThreshold: number;
  /** 描边采样半径（像素） */
  edgeWidth: number;
  /** 单色纸墨 0..1 */
  mono: number;

  // ── L2 半调（屏幕空间网点，在合成 pass 做）──
  /** 半调网点强度 0..1。> 0 时合成 pass 切到 halftone（网点）而不是点云自带点画 */
  halftone: number;
  /** 网点 cell 大小（屏幕像素） */
  halftoneCell: number;
  /** 网屏角度（度，印刷常用 15/45/75） */
  halftoneAngle: number;

  // ── L2 铜版画排线（屏幕空间排线，同样在合成 pass 做）──
  /** 排线强度 0..1。> 0 时合成 pass 切到 engraving */
  engraving: number;
  /** 排线层数（2~4；层间角度差 45°） */
  hatchLayers: number;
  /** 排线间距（屏幕像素） */
  hatchSpacing: number;
  /** 第一层排线角度（度） */
  hatchAngle: number;
  /** 色阶数（posterize，3~5） */
  posterizeLevels: number;

  // ── L3 时间层 ──
  /** 沸腾线开关 */
  boil: boolean;

  // ── L4 后期层 ──
  /** 空气透视强度 0..1 */
  fogAmount: number;
  /** 暗角 0..1 */
  vignette: number;
  /** 纸颗粒 0..1 */
  grain: number;
  /** 纸色 / 背景色（#rrggbb） */
  paper: string;
  /**
   * 画布底色（#rrggbb）—— 和纸色分开。
   *
   * paper 是「点缝里透出来的纸」，参与点的空隙混色；
   * bgColor 是整张画布在没有任何点处的底色（导出图的底）。
   * 想把作品放到深色/彩色底上时改这个，纸感不受影响。
   * 未设置时等价于纸色。
   */
  bgColor: string;
  /** 墨色（#rrggbb）：duotone 的暗端 + 描边色 */
  ink: string;

  // ── 相机 ──
  /**
   * 相机运动模式：`true` = **视差平移**（默认，3D Photo / Live2D 做法），`false` = 旋转摆动。
   *
   * 平板数据（真实插画的深度）**不能旋转**：哪怕 1°，矩形的边就不再平行于画面，
   * 眼睛立刻读成"一块斜着的板"。视差平移只改变机位、不改变朝向 →
   * 近层/远层错动产生深度感，而所有平板始终平行于画面。
   */
  parallaxMode: boolean;
  /**
   * 视差幅度（场景宽度的百分比，0~15，默认 4）。
   * 相机横移量 = 场景宽度 × pct%；0 = 画面完全静止（等于原图）。
   */
  parallaxAmount: number;
  /** 自动运动开关（视差模式下来回平移；旋转模式下来回摆动） */
  autoSway: boolean;
  /**
   * 自由旋转（默认开）：手动左键拖拽不受任何角度限制（只留 ±89° 俯仰防万向锁）。
   * 关掉则退回「基准 ± 摆动幅度」的限位（旧的平板防穿帮行为）。
   * 注意：它**只影响手动旋转**；自动运动是平移还是旋转仍由 parallaxMode 决定。
   */
  freeRotate: boolean;
  /**
   * 旋转模式下的摆动幅度上限（度）：仅 `parallaxMode = false` 时生效。
   * 0 = 锁死正面；默认 15。
   */
  swayAmplitude: number;
  /**
   * 取景基准视角：false = **正面**（azimuth 0 / elevation 0，正对 -Z）。
   *
   * 深度图反投影的点云约定是 +Z 朝观察者 / +X 右 / +Y 上（docs/selection.md §4.4），
   * 所以"插画本来的样子"就是正面视角；3/4 视角等于把画斜过来看，只适合看 3D fixture。
   * 打开这个开关就把基准切到 3/4（调试用，也可用 `?baseView=threeQuarter`）。
   */
  threeQuarterView: boolean;
}

export type NumericParamKey = {
  [K in keyof RenderParams]: RenderParams[K] extends number ? K : never;
}[keyof RenderParams];

export const PRESET_KEYS = [
  'color-print',
  'stipple-print',
  'halftone-manga',
  'engraving',
  'ink-edge',
  'raw',
] as const;
export type PresetKey = (typeof PRESET_KEYS)[number];

export interface Preset {
  readonly label: string;
  readonly params: RenderParams;
}

/** 所有预设共用的「默认关闭」参数，避免每个预设重复写一遍 */
const DEFAULTS = {
  // 相机 FOV：3° ≈ 正交（见 RenderParams.cameraFov）。42 是旧的透视值，会呈放射状扇形。
  cameraFov: 3,
  halftone: 0,
  halftoneCell: 8,
  halftoneAngle: 45,
  engraving: 0,
  hatchLayers: 3,
  hatchSpacing: 5,
  hatchAngle: 45,
  posterizeLevels: 4,
  ink: '#1b1a17',
  threeQuarterView: false,
  parallaxMode: true,
  parallaxAmount: 4,
  freeRotate: true,
  colorMode: 1,
  colorPosterize: 5,
  colorHalftone: 0,
  colorSaturation: 1.2,
  renderMode: 'cloud', // 产品路径 = 点云；图像模式只做对照
  inkSource: 1,
  inkPolarity: 0,
  inkGain: 8,
  stippleScale: 4,
} as const;

/**
 * §7 的预设组合。
 *
 * 已实现：点画印刷（§7 第 1 套）、半调漫画（第 3 套）、铜版画（第 2 套），
 * 外加两个调试档（线描 / 原色调试）。
 * 未做：油画（要笔触 quad 基元）、像素立体书（要 voxel/InstancedMesh 基元）、梦境星空（加法混合粒子）。
 */
export const PRESETS: Record<PresetKey, Preset> = {
  // ── 彩色点画（产品主路径）：保留每点颜色，印刷质感做叠加层 ──
  'color-print': {
    label: '彩色点画',
    params: {
      ...DEFAULTS,
      pointSize: 3, // 最终配方（加载点云时仍会被自适应取景重算）
      // 软边在这里是**用来填点缝、提高饱和度的**，不是柔化：硬边会让点缝全露纸色 → 反而更淡。
      softness: 0.25,
      density: 1.0,
      stipple: 0.0, // 点密度不再由亮度决定（colorMode 会强制归零）
      densityGamma: 1.0,
      edgeStrength: 0.0, // 线稿已经在每点颜色里，描边只会加粗黑边
      edgeThreshold: 0.35,
      edgeWidth: 2.0,
      fogAmount: 0.0,
      mono: 0.0,
      grain: 0.02,
      vignette: 0.12,
      swayAmplitude: 15,
      autoSway: true,
      halftone: 0.0,
      halftoneCell: 8,
      halftoneAngle: 45,
      engraving: 0.0,
      boil: false,
      paper: '#ffffff', // 纯白纸：暖纸色会吃掉颜色饱和度
      bgColor: '#ffffff',
      colorMode: 1,
      colorPosterize: 5,
      colorHalftone: 0,
      colorSaturation: 1.2,
    },
  },
  'stipple-print': {
    label: '点画印刷',
    params: {
      ...DEFAULTS,
      pointSize: 2.6,
      softness: 0.6,
      density: 1.0,
      stipple: 0.9,
      colorMode: 0, // 单色墨量路径（亮度→点密度）
      densityGamma: 1.3,
      edgeStrength: 1.0,
      edgeThreshold: 0.3,
      edgeWidth: 2.0,
      mono: 1.0,
      boil: true,
      fogAmount: 0.75,
      vignette: 0.28,
      grain: 0.03,
      paper: '#f2ece1',
      bgColor: '#f2ece1',
      autoSway: true,
      swayAmplitude: 15,
    },
  },
  /**
   * §2.4 半调网点 + 粗描边 = 黑白漫画。
   *
   * 点云本身**不做点画**（stipple=0），它只负责提供一张连续调图像（颜色 pass），
   * 网点化是合成 pass 干的（见 styles/halftone.frag.glsl 文件头）。
   */
  'halftone-manga': {
    label: '半调漫画',
    params: {
      ...DEFAULTS,
      pointSize: 2.2, // 小点 → 颜色 pass 更接近连续调，网点才连续
      softness: 0.35,
      density: 1.0,
      stipple: 0.0,
      colorMode: 0,
      densityGamma: 1.2,
      edgeStrength: 1.6, // 粗描边
      edgeThreshold: 0.22,
      edgeWidth: 2.6,
      mono: 0.0,
      halftone: 1.0,
      halftoneCell: 8,
      halftoneAngle: 45,
      boil: true, // 抖动换序：网点网格按相位重排
      fogAmount: 0.18,
      vignette: 0.16,
      grain: 0.02,
      paper: '#f6f2e8',
      bgColor: '#f6f2e8',
      autoSway: true,
      swayAmplitude: 12,
    },
  },
  /**
   * §2.2 排线 + §7 第 2 套铜版画：3 层交叉排线 + posterize(4) + duotone（纸黄/墨褐）。
   *
   * 排线同样在合成 pass 做（理由和半调一样：排线是对连续调图像做网屏处理）。
   * 没有时间层（§7 铜版画 = 无 L3），所以 boil 关掉。
   */
  engraving: {
    label: '铜版画',
    params: {
      ...DEFAULTS,
      pointSize: 2.6, // \u5c0f\u70b9 \u2192 \u989c\u8272 pass \u8fd1\u4f3c\u8fde\u7eed\u8c03
      softness: 0.4,
      density: 1.0,
      stipple: 0.0,
      colorMode: 0,
      densityGamma: 1.3, // 屏幕化的调子曲线
      edgeStrength: 1.1,
      edgeThreshold: 0.3,
      edgeWidth: 2.0,
      mono: 0.0,
      engraving: 1.0,
      hatchLayers: 3,
      hatchSpacing: 8,
      hatchAngle: 45,
      posterizeLevels: 4,
      boil: false,
      fogAmount: 0.2,
      vignette: 0.2,
      grain: 0.03,
      paper: '#efe1c2', // 纸黄
      bgColor: '#efe1c2',
      ink: '#2e1c10', // 墨褐
      autoSway: true,
      swayAmplitude: 12,
    },
  },
  'ink-edge': {
    label: '线描',
    params: {
      ...DEFAULTS,
      pointSize: 2.2,
      softness: 0.5,
      density: 1.0,
      stipple: 0.85,
      colorMode: 0,
      densityGamma: 2.0, // 中间调调亮，把画面让给线条
      edgeStrength: 1.4,
      edgeThreshold: 0.3,
      edgeWidth: 1.9,
      mono: 1.0,
      boil: false,
      fogAmount: 0.4,
      vignette: 0.2,
      grain: 0.02,
      paper: '#f4efe6',
      bgColor: '#f4efe6',
      autoSway: false,
      swayAmplitude: 15,
    },
  },
  raw: {
    label: '原色调试',
    params: {
      ...DEFAULTS,
      pointSize: 4.2,
      softness: 0.5,
      density: 1.0,
      stipple: 0.0,
      densityGamma: 1.0,
      edgeStrength: 0.0,
      edgeThreshold: 0.3,
      edgeWidth: 1.5,
      mono: 0.0,
      boil: false,
      fogAmount: 0.0,
      vignette: 0.0,
      grain: 0.0,
      paper: '#14161a',
      bgColor: '#14161a',
      autoSway: true,
      swayAmplitude: 15,
      // 调试档：用 3/4 视角看 fixture 的立体感
      threeQuarterView: true,
    },
  },
};

export const DEFAULT_PRESET: PresetKey = 'color-print';

/** 深拷贝一份参数，避免预设对象被运行时改掉 */
export function cloneParams(params: RenderParams): RenderParams {
  return { ...params };
}

// ── 点云生成参数（不是渲染参数！改这些要重新请求 /api/infer）──────────────
export interface GenParams {
  /** 焦距系数：fx = fy = focalFactor · max(W,H)。小 = 广角（点云更厚），大 = 长焦（更薄） */
  focalFactor: number;
  /** 点数档位（Go 侧硬上限 5,000,000；受 work_res 候选网格限制） */
  maxPoints: number;
  /** 深度边缘剔除阈值；0 = 自动（p99） */
  edgeThresh: number;
  /** 采样模式：structure（跟随图像结构）/ depthpixel（对照基线） */
  sampleMode: 'structure' | 'depthpixel';
  /** 工作分辨率；0 = 自动（clamp(原图长边/2, 512, 2048)） */
  workRes: number;
  /**
   * z 轴（垂直图片方向）压缩系数：z = -(d - dRef)·depthScale。
   *
   * 正交投影下 z 只决定「浮雕厚度」：1 = 原始厚度，0.5 = 一半，0.2 = 很薄。
   * 越小越接近原图、侧转时露出的背景空洞越小；0.2~0.5 观感最稳。
   */
  depthScale: number;
}

export const GEN_DEFAULTS: GenParams = {
  focalFactor: 1,
  // 默认 100 万：与 200 万同为 144fps（瓶颈在传输不在渲染），端到端只多 0.4s，
  // 细节明显好于 40 万。Go 侧 DefaultMaxPoints 保持 40 万（纯 API 兜底），web 显式传值。
  maxPoints: 1000000,
  edgeThresh: 0,
  sampleMode: 'structure',
  workRes: 0,
  depthScale: 0.5,
};

/** 点数档位（按钮） */
export const MAX_POINT_TIERS: ReadonlyArray<{ value: number; label: string }> = [
  { value: 400000, label: '40 万' },
  { value: 1000000, label: '100 万' },
  { value: 2000000, label: '200 万' },
];
