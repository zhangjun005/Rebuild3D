/**
 * 渲染参数 —— 与 docs/rendering-styles.md §6 的参数清单对齐（第一阶段只开「点画印刷」用得上的那些）。
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
}

export type NumericParamKey = {
  [K in keyof RenderParams]: RenderParams[K] extends number ? K : never;
}[keyof RenderParams];

export const PRESET_KEYS = ['stipple-print', 'halftone-manga', 'ink-edge', 'raw'] as const;
export type PresetKey = (typeof PRESET_KEYS)[number];

export interface Preset {
  readonly label: string;
  readonly params: RenderParams;
}

/**
 * §7「六套预设」的第一套（也是本阶段唯一实现完整的一套）。
 *
 * 剩下的（铜版画/半调漫画/油画/像素立体书/梦境星空）留到第二阶段，
 * 但 L1/L2/L3/L4 四个层的钩子都已经在 viewer + shader 里留好了位置。
 */
export const PRESETS: Record<PresetKey, Preset> = {
  'stipple-print': {
    label: '点画印刷',
    params: {
      pointSize: 2.6,
      softness: 0.6,
      density: 1.0,
      stipple: 0.9,
      densityGamma: 1.3,
      edgeStrength: 1.0,
      edgeThreshold: 0.14,
      edgeWidth: 2.0,
      mono: 1.0,
      halftone: 0,
      halftoneCell: 7,
      halftoneAngle: 45,
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
   * 注意这里的组合逻辑：点云本身**不做点画**（stipple=0），它只负责提供一张连续调图像
   * （颜色 pass），网点化是合成 pass 干的（见 styles/halftone.frag.glsl 文件头）。
   * mono=0 保留原色只是为了拿到亮度；网点输出是纯 墨/纸 两色。
   */
  'halftone-manga': {
    label: '半调漫画',
    params: {
      pointSize: 2.2, // 小点 → 颜色 pass 更接近连续调，网点才连续
      softness: 0.35,
      density: 1.0,
      stipple: 0.0,
      densityGamma: 1.2, // 屏幕化的明暗曲线：>1 拉开对比（暗部点更大）
      edgeStrength: 1.6, // 粗描边
      edgeThreshold: 0.10,
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
  'ink-edge': {
    label: '线描',
    params: {
      pointSize: 2.2,
      softness: 0.5,
      density: 1.0,
      stipple: 0.85,
      densityGamma: 2.0, // 中间调调亮，把画面让给线条
      edgeStrength: 1.4,
      edgeThreshold: 0.12,
      edgeWidth: 1.9,
      mono: 1.0,
      halftone: 0,
      halftoneCell: 7,
      halftoneAngle: 45,
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
      pointSize: 4.2,
      softness: 0.5,
      density: 1.0,
      stipple: 0.0,
      densityGamma: 1.0,
      edgeStrength: 0.0,
      edgeThreshold: 0.3,
      edgeWidth: 1.5,
      mono: 0.0,
      halftone: 0,
      halftoneCell: 7,
      halftoneAngle: 45,
      boil: false,
      fogAmount: 0.0,
      vignette: 0.0,
      grain: 0.0,
      paper: '#14161a',
      autoSway: true,
      swayAmplitude: 15,
    },
  },
};

export const DEFAULT_PRESET: PresetKey = 'stipple-print';

/** 深拷贝一份参数，避免预设对象被运行时改掉 */
export function cloneParams(params: RenderParams): RenderParams {
  return { ...params };
}
