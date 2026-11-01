/**
 * 渲染参数 —— 与 docs/rendering-styles.md §6 的参数清单对齐。
 *
 * 命名约定：TS 侧用 camelCase，uniform 侧用 uPascalCase，两者在 Viewer#applyParams 里一一对应。
 */

export interface RenderParams {
  // ── L1 基元层 ──
  /** 点大小：参考深度处的屏幕像素直径 */
  pointSize: number;
  /** 软边：0 = 硬边圆点，1 = 最软 */
  softness: number;
  /** 点密度（点数比例）：逐点抽稀，空间均匀 */
  density: number;

  // ── L2 风格层 ──
  /** 点画强度 0..1 */
  stipple: number;
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
  /** 墨色（#rrggbb）：duotone 的暗端 + 描边色 */
  ink: string;

  // ── 相机 ──
  /**
   * 自动**摆动**（不是转圈）：真实插画深度只是剪影，转满一圈会露馅（go/no-go 结论）。
   * 摆动是往返运动，录制时一个周期 T 内走完整来回，state(T) ≡ state(0) 仍成立。
   */
  autoSway: boolean;
  /**
   * 摆动 / 环绕的幅度上限（度）：方位角与俯仰角都限制在取景基准的 ±amp 内。
   * 0 = 锁死正面（只用来看平面剪影）；默认 15（±15° 刚好不露主体板边缘）。
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
} as const;

/**
 * §7 的预设组合。
 *
 * 已实现：点画印刷（§7 第 1 套）、半调漫画（第 3 套）、铜版画（第 2 套），
 * 外加两个调试档（线描 / 原色调试）。
 * 未做：油画（要笔触 quad 基元）、像素立体书（要 voxel/InstancedMesh 基元）、梦境星空（加法混合粒子）。
 */
export const PRESETS: Record<PresetKey, Preset> = {
  'stipple-print': {
    label: '点画印刷',
    params: {
      ...DEFAULTS,
      pointSize: 2.6,
      softness: 0.6,
      density: 1.0,
      stipple: 0.9,
      densityGamma: 1.3,
      edgeStrength: 1.0,
      edgeThreshold: 0.14,
      edgeWidth: 2.0,
      mono: 1.0,
      boil: true,
      fogAmount: 0.75,
      vignette: 0.28,
      grain: 0.03,
      paper: '#f2ece1',
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
      densityGamma: 1.2,
      edgeStrength: 1.6, // 粗描边
      edgeThreshold: 0.1,
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
      densityGamma: 1.3, // 屏幕化的调子曲线
      edgeStrength: 1.1,
      edgeThreshold: 0.14,
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
      densityGamma: 2.0, // 中间调调亮，把画面让给线条
      edgeStrength: 1.4,
      edgeThreshold: 0.12,
      edgeWidth: 1.9,
      mono: 1.0,
      boil: false,
      fogAmount: 0.4,
      vignette: 0.2,
      grain: 0.02,
      paper: '#f4efe6',
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
      autoSway: true,
      swayAmplitude: 15,
      // 调试档：用 3/4 视角看 fixture 的立体感
      threeQuarterView: true,
    },
  },
};

export const DEFAULT_PRESET: PresetKey = 'stipple-print';

/** 深拷贝一份参数，避免预设对象被运行时改掉 */
export function cloneParams(params: RenderParams): RenderParams {
  return { ...params };
}
