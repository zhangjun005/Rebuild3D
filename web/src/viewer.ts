/**
 * 渲染核心 —— 四 pass 的点画印刷管线。
 *
 *   pass 1  深度     点云 → HalfFloat target，写归一化线性深度（背景 = 1.0 = 最远）
 *   pass 2  平滑     双边 3×3 平滑深度（§9：先平滑再求梯度，否则描边闪）
 *   pass 3  颜色     点云 → 纸色底的 color target（软边圆片 + blue noise 点画 + 空气透视）
 *   pass 4  合成     色彩 + sobel 深度描边 + 暗角 + 纸颗粒 → 屏幕
 *
 * 为什么深度要单独一遍而不是用主 pass 的 depth buffer：
 *   a) 需要浮点（HalfFloat）存归一化线性深度，8 位会带出条带 → 描边闪；
 *   b) 点云深度有洞，可以在深度 pass 里把点略微放大封洞，而不影响画面；
 *   c) 描边不随「点密度」塌陷 —— 密度调低时线仍然连续，这正是想要的美术控制。
 *
 * 颜色空间：ColorManagement 关闭 + outputColorSpace = LinearSRGBColorSpace，
 * 全链路在「显示空间」里算。风格化渲染不是物理渲染，避免两次 8 位量化与意外的 gamma。
 */

import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';

import depthFrag from './styles/depth.frag.glsl?raw';
import engravingFrag from './styles/engraving.frag.glsl?raw';
import halftoneFrag from './styles/halftone.frag.glsl?raw';
import imagePrintFrag from './styles/image-print.frag.glsl?raw';
import imageQuadFrag from './styles/image-quad.frag.glsl?raw';
import depthVert from './styles/depth.vert.glsl?raw';
import pointsVert from './styles/points.vert.glsl?raw';
import postEdgeFrag from './styles/post-edge.frag.glsl?raw';
import quadVert from './styles/quad.vert.glsl?raw';
import smoothFrag from './styles/smooth-depth.frag.glsl?raw';
import stippleFrag from './styles/stipple-print.frag.glsl?raw';

import type { IlcCloud } from './ilcl';
import { cloneParams, DEFAULT_PRESET, PRESETS, type PresetKey, type RenderParams } from './params';

// 全链路显示空间（理由见文件头）
THREE.ColorManagement.enabled = false;

/**
 * 默认参考深度：uPointSize 是「这个视距下的像素直径」。
 * 装上点云后会按包围盒重算（真实场景尺度不同，不能写死）。
 */
const REF_DEPTH_DEFAULT = 4.0;
/** 风格关心的深度区间：同时用于空气透视与描边归一化。装上点云后按取景距离重算 */
const DEPTH_NEAR_DEFAULT = 2.5;
const DEPTH_FAR_DEFAULT = 8.0;
/** 深度 pass 把点放大的系数（封住点间隙，避免描边被点阵打碎） */
const DEPTH_POINT_GROW = 4.0;

/** 取景：核心半径取「到中心距离的 75 分位」，避免大片地面/背景点把相机拉飞 */
const FRAME_RADIUS_PERCENTILE = 0.75;
/**
 * 取景留边系数（> 1 = 留白，< 1 = 故意裁边）。
 *
 * 历史：fixture 时代取 0.9（切片地面、让主体占满画面）。
 * 真实插画**不行**——插画的边缘就是构图，按 0.9 会裁掉一圈（实测裁掉约 10%）。
 * 现在按实际投影点套框，统一留 4% 白边。
 */
const FRAME_MARGIN = 1.04;
/** 点大小自适应：点径 ≈ 屏幕点间距 × 这个系数（0.5 → 相邻点略微交叠） */
const POINT_SIZE_PER_SPACING = 0.5;
/** 自适应点大小的上下限（CSS 像素） */
const POINT_SIZE_MIN = 1.2;
const POINT_SIZE_MAX = 8.0;

// ── 相机限位 / 摆动 ────────────────────────────────────────────────────────
/**
 * 摆动时俯仰幅度 = 方位幅度 × 这个比例。
 * 真实插画的深度只是「剪影 + 平板」，转太多会露出板的边缘；
 * 方位摆动最能看出体积感，俯仰只给一点点，避免看到板的上下边缘。
 */
const SWAY_PITCH_RATIO = 0.5;
/** 相机仰角的绝对上下限（度）：只做防翻转的保护，不再按地面高度硬卡（真实点云没有"地面"） */
const ELEVATION_MIN_DEG = -80;
const ELEVATION_MAX_DEG = 86;
/** 实时预览的摆动周期（秒）。录制时周期由 N 帧决定，不用这个 */
const LIVE_SWAY_PERIOD = 8;
/** 摆动幅度的参数上限（度） */
const SWAY_MAX_DEG = 45;
/** 视差幅度的参数上限（场景宽度百分比） */
const PARALLAX_MAX_PERCENT = 15;

/**
 * 图像模式的描边阈值缩放：uEdgeThreshold 的默认值是按**深度梯度**（0..1 归一化）定的，
 * 图像亮度梯度是另一个尺度（线稿 1~2px、对比度各不相同）→ 乘一个系数共用同一个参数。
 */
const IMAGE_EDGE_THRESHOLD_SCALE = 0.4;

/**
 * 取景基准方向。
 *
 * **正面**（默认）：相机在 +Z 看向 -Z。深度图反投影的点云约定是
 * +Z 朝观察者 / +X 右 / +Y 上（docs/selection.md §4.4），所以"插画本来的样子"
 * 就是这个正面视角；3/4 视角等于把画斜过来看。
 *
 * 3/4：只适合看 fixture（球+立方体）的立体感，调试用开关。
 */
/**
 * 取景基准方向 = 相机相对点云中心的方向（相机在 center + dir·distance）。
 *
 * 约定（docs/selection.md §4.4）：深度 d 是距离（越大越远），Go 反投影输出 z = -d，
 * 所以点云的 z 越大 = 越靠近观察者。相机在 +Z 沿 -Z 看（three.js 默认朝向）就是「正对画面」。
 *
 * 曾经 Go 输出 z = d（远处 z 更大）而渲染层把相机放在 -Z 才「看起来对」——那是两端各自
 * 合理、合起来双重取反的经典坑（雾/深度上墨/视差三条症状同时反向）。符号现在统一在 Go 侧，
 * 渲染层用标准相机。验证方式：量 z，主体应比背景大（更靠近观察者）；别靠"看着好看"判断。
 */
const BASE_DIR_FRONT = new THREE.Vector3(0, 0, 1);
const BASE_DIR_THREE_QUARTER = new THREE.Vector3(0.34, 0.36, 0.87);

/** 沸腾：一个可回绕循环的「逻辑帧」数（60 fps 下 0.8 s） */
const BOIL_CYCLE_FRAMES = 48;
/** 沸腾重绘频率（§9：8~12 fps 最像手绘） */
const BOIL_FPS = 10;
/** 沸腾：描边采样抖动幅度（像素） */
const BOIL_LINE_JITTER = 1.6;

/** 墨色（单色纸墨的暗端 + 描边色） */
const INK_HEX = '#1b1a17';

export interface ViewerStats {
  fps: number;
  frame: number;
  points: number;
  /** 一次主渲染里的 draw call 数（4 个 pass） */
  drawCalls: number;
  /** 当前沸腾相位（0 = 未开） */
  boilPhase: number;
  width: number;
  height: number;
  /** 取景：相机到核心中心的距离 */
  cameraDistance: number;
  /** 取景：核心半径（75 分位） */
  coreRadius: number;
  /** 当前点大小（CSS px @ 参考深度） */
  pointSize: number;
}

type NumericUniform = THREE.IUniform<number>;

interface PointUniforms {
  uPointSize: NumericUniform;
  uScale: NumericUniform;
  uMinSize: NumericUniform;
  uMaxSize: NumericUniform;
  uFogNear: NumericUniform;
  uFogFar: NumericUniform;
  uDensity: NumericUniform;
  uPaper: THREE.IUniform<THREE.Color>;
  uInk: THREE.IUniform<THREE.Color>;
  uSoftness: NumericUniform;
  uStipple: NumericUniform;
  uDensityGamma: NumericUniform;
  uMono: NumericUniform;
  uFogAmount: NumericUniform;
  uBoilSeed: NumericUniform;
  uToneLow: NumericUniform;
  uToneHigh: NumericUniform;
}

interface DepthUniforms {
  uPointSize: NumericUniform;
  uScale: NumericUniform;
  uGrow: NumericUniform;
  uDepthNear: NumericUniform;
  uDepthFar: NumericUniform;
}

interface SmoothUniforms {
  tDepth: THREE.IUniform<THREE.Texture | null>;
  uTexel: THREE.IUniform<THREE.Vector2>;
  uRangeTol: NumericUniform;
}

/** 点画印刷的合成 pass uniform */
interface CompositeUniforms {
  tColor: THREE.IUniform<THREE.Texture | null>;
  tDepth: THREE.IUniform<THREE.Texture | null>;
  tNoise: THREE.IUniform<THREE.Texture | null>;
  uTexel: THREE.IUniform<THREE.Vector2>;
  uEdgeStrength: NumericUniform;
  uEdgeThreshold: NumericUniform;
  uEdgeWidth: NumericUniform;
  uInk: THREE.IUniform<THREE.Color>;
  uVignette: NumericUniform;
  uGrain: NumericUniform;
  uBoilJitter: THREE.IUniform<THREE.Vector2>;
  uBoilFlicker: NumericUniform;
  // 彩色点画（保留原色 + 印刷叠加层）
  uColorMode: NumericUniform;
  uColorPosterize: NumericUniform;
  uColorHalftone: NumericUniform;
  uColorSaturation: NumericUniform;
  uColorCell: NumericUniform;
  uColorAngle: NumericUniform;
  tPaperNoise: THREE.IUniform<THREE.Texture | null>;
}

/** 墨量映射（两种模式共用）：来源/极性/增益/平均亮度 */
interface InkUniforms {
  uInkSource: NumericUniform;
  uInkPolarity: NumericUniform;
  uInkGain: NumericUniform;
  uMeanLuminance: NumericUniform;
}

/** 半调漫画的合成 pass uniform（多了网点相关的那几个） */
interface HalftoneUniforms extends CompositeUniforms, InkUniforms {
  uPaper: THREE.IUniform<THREE.Color>;
  uHalftoneStrength: NumericUniform;
  uHalftoneCell: NumericUniform;
  uHalftoneAngle: NumericUniform;
  uDensityGamma: NumericUniform;
  uHalftoneOffset: THREE.IUniform<THREE.Vector2>;
  uToneRange: THREE.IUniform<THREE.Vector2>;
}

/** 铜版画的合成 pass uniform（排线相关） */
interface EngravingUniforms extends CompositeUniforms, InkUniforms {
  uPaper: THREE.IUniform<THREE.Color>;
  uEngravingStrength: NumericUniform;
  uHatchLayers: NumericUniform;
  uHatchSpacing: NumericUniform;
  uHatchAngle: NumericUniform;
  uPosterizeLevels: NumericUniform;
  uDensityGamma: NumericUniform;
  uScreenOffset: THREE.IUniform<THREE.Vector2>;
  uToneRange: THREE.IUniform<THREE.Vector2>;
}

/** 配置 blue noise 纹理（点画阈值 + 纸颗粒共用） */
export function configureBlueNoiseTexture(texture: THREE.Texture): THREE.Texture {
  texture.wrapS = THREE.RepeatWrapping;
  texture.wrapT = THREE.RepeatWrapping;
  texture.magFilter = THREE.NearestFilter;
  texture.minFilter = THREE.NearestFilter;
  texture.generateMipmaps = false;
  texture.colorSpace = THREE.NoColorSpace;
  texture.needsUpdate = true;
  return texture;
}

/**
 * 每点随机数 [0,1)：点云加载时一次性生成（确定性 mulberry32，便于复现）。
 * 用它代替 shader 里的位置哈希做点画/密度抽稀——真实深度点云是规则网格，
 * 位置哈希在格点上会相关，屏幕上出现条带状摩尔纹。
 */
function createPointRandoms(count: number): Float32Array {
  const randoms = new Float32Array(count);
  let state = 0x9e3779b9;
  for (let i = 0; i < count; i++) {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    randoms[i] = ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  }
  return randoms;
}

/** 确定性 0..1 哈希：沸腾相位只依赖帧号，保证 GIF 循环不跳帧 */
function hash01(n: number): number {
  let x = Math.imul(n ^ 0x9e3779b9, 0x85ebca6b);
  x ^= x >>> 13;
  x = Math.imul(x, 0xc2b2ae35);
  x ^= x >>> 16;
  return (x >>> 0) / 4294967296;
}

export class Viewer {
  readonly params: RenderParams;

  /** 每秒回调一次左右的统计（给 HUD 用） */
  onStats: ((stats: ViewerStats) => void) | null = null;
  /** 点云加载完成后回调（HUD 显示来源） */
  onSource: ((source: string) => void) | null = null;

  private readonly renderer: THREE.WebGLRenderer;
  private readonly scene = new THREE.Scene();
  private readonly camera: THREE.PerspectiveCamera;
  private readonly controls: OrbitControls;

  private readonly pointMaterial: THREE.ShaderMaterial;
  private readonly depthMaterial: THREE.ShaderMaterial;
  private readonly smoothMaterial: THREE.ShaderMaterial;
  private readonly compositeMaterial: THREE.ShaderMaterial;
  /** 半调漫画的合成 pass（params.halftone > 0 时使用） */
  private readonly halftoneMaterial: THREE.ShaderMaterial;
  /** 墨量映射 uniform（半调/排线/图像模式共用同一份值） */
  private readonly inkUniforms: InkUniforms;
  /** 铜版画的合成 pass（params.engraving > 0 时使用） */
  private readonly engravingMaterial: THREE.ShaderMaterial;
  /**
   * 图像模式（不碰深度）：原图 → 全屏 quad → 图像合成 pass。
   * 第一层是「要不要立体」的选择，和画风预设正交。
   */
  private readonly imageQuadMaterial: THREE.ShaderMaterial;
  private readonly imagePrintMaterial: THREE.ShaderMaterial;
  private readonly imageQuadUniforms: {
    tImage: THREE.IUniform<THREE.Texture | null>;
    uImageScale: THREE.IUniform<THREE.Vector2>;
    uPaper: THREE.IUniform<THREE.Color>;
  };
  private readonly imagePrintUniforms: Record<string, THREE.IUniform>;
  /** 当前图像（null = 没上传过，图像模式不可用） */
  private imageTexture: THREE.Texture | null = null;
  private imageAspect = 1;
  /** 图像平均亮度（主体极性"自动"用） */
  private imageMeanLuminance = 0.5;
  /** 图像占屏幕宽/高的比例（contain 摆放） */
  private readonly imageScale = new THREE.Vector2(1, 1);

  private readonly pointUniforms: PointUniforms;
  private readonly depthUniforms: DepthUniforms;
  private readonly smoothUniforms: SmoothUniforms;
  private readonly compositeUniforms: CompositeUniforms;
  private readonly halftoneUniforms: HalftoneUniforms;
  private readonly engravingUniforms: EngravingUniforms;

  private readonly quadScene = new THREE.Scene();
  private readonly quadCamera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1);
  private readonly quadMesh: THREE.Mesh;
  private readonly quadGeometry = new THREE.PlaneGeometry(2, 2);

  private colorTarget: THREE.WebGLRenderTarget | null = null;
  private depthTarget: THREE.WebGLRenderTarget | null = null;
  private smoothTarget: THREE.WebGLRenderTarget | null = null;
  private readonly depthTextureType: THREE.TextureDataType;

  private colorPoints: THREE.Points | null = null;
  private depthPoints: THREE.Points | null = null;
  private readonly paperColor = new THREE.Color();
  /** 画布底色（与纸色分开）：只用于清屏，不参与点着色 */
  private readonly canvasBgColor = new THREE.Color();
  private readonly inkColor = new THREE.Color(INK_HEX);

  private rafId = 0;
  private frame = 0;
  private lastTickAt = 0;
  /** 沸腾逻辑帧计：按 60 fps 折算，落在 [0, BOIL_CYCLE_FRAMES) 内（可回绕） */
  private boilClock = 0;
  private fps = 0;
  private fpsFrames = 0;
  private fpsWindowStart = 0;
  private lastStatsAt = 0;
  private boilPhase = 0;
  private disposed = false;

  /** 画布（导出 PNG 用） */
  readonly canvas: HTMLCanvasElement;
  /** 导出时的临时尺寸覆盖（null = 跟窗口走；只在 exportPng 期间非空） */
  private exportSize: { width: number; height: number } | null = null;

  // ── 取景基准位姿（摆动与限位都围绕它）──
  /** 取景基准方位角（rad） */
  private baseAzimuth = 0;
  /** 取景基准仰角（rad） */
  private baseElevation = 0;
  /** 实时摆动时钟（秒，mod LIVE_SWAY_PERIOD） */
  private swayClock = 0;
  /**
   * 视差模式的基准位姿：位置 + **朝向**（朝向在整个视差运动中保持不变）。
   * 旋转模式的摆动会改朝向；视差模式只平移相机与 target，四元数原样复用。
   */
  private readonly parallaxBasePosition = new THREE.Vector3();
  private readonly parallaxBaseTarget = new THREE.Vector3();
  private readonly parallaxBaseQuaternion = new THREE.Quaternion();
  private readonly parallaxRight = new THREE.Vector3(1, 0, 0);
  private readonly parallaxUp = new THREE.Vector3(0, 1, 0);
  /** 场景宽度（= 2×核心半径）：视差幅度按它的百分比算 */
  private sceneWidth = 1;
  /** 当前运动模式是否视差（用于检测开关变化） */
  private parallaxMode = true;
  /** 当前点云（基准视角切换时要重新取景） */
  private cloudGeometry: THREE.BufferGeometry | null = null;
  private cloudPosition: THREE.BufferAttribute | null = null;
  /** 当前基准视角是否 3/4（用于检测开关变化） */
  private threeQuarterView = false;
  /** 自动色调范围（点云亮度 5~95 分位）：真实插画整片偏亮/偏暗时保证疏密仍然可用 */
  private toneLow = 0;
  private toneHigh = 1;
  /** 参数被内部改动（例如拖拽自动关掉摆动）时通知面板同步 */
  onInternalParamChange: (() => void) | null = null;

  // ── 随点云变化的取景 / 深度参数（真实点云尺度不同，不能写死）──
  /** 点大小换算用的参考视距（uPointSize 是"这个距离下的像素直径"） */
  private refDepth = REF_DEPTH_DEFAULT;
  /** 空气透视与描边归一化用的深度区间 */
  private depthNear = DEPTH_NEAR_DEFAULT;
  private depthFar = DEPTH_FAR_DEFAULT;
  /** 上次取景信息（HUD / 调试用） */
  private frameInfo: { center: THREE.Vector3; radius: number; distance: number } | null = null;

  constructor(canvas: HTMLCanvasElement, blueNoise: THREE.Texture) {
    this.params = cloneParams(PRESETS[DEFAULT_PRESET].params);
    this.paperColor.set(this.params.paper);
    this.canvas = canvas;

    this.renderer = new THREE.WebGLRenderer({
      canvas,
      antialias: false,
      alpha: false,
      powerPreference: 'high-performance',
    });
    // 不做 sRGB 编码：颜色是作者意图色，直接上屏（见文件头）
    this.renderer.outputColorSpace = THREE.LinearSRGBColorSpace;
    this.renderer.toneMapping = THREE.NoToneMapping;
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));

    // 深度/平滑 target 想用 HalfFloat，先看驱动给不给（§9「深度图精度不足」的兜底）
    const canHalfFloat =
      this.renderer.extensions.has('EXT_color_buffer_half_float') ||
      this.renderer.extensions.has('EXT_color_buffer_float');
    this.depthTextureType = canHalfFloat ? THREE.HalfFloatType : THREE.UnsignedByteType;
    if (!canHalfFloat) {
      console.warn('[illust3d] 没有 EXT_color_buffer_half_float，深度图退回 8 位，描边可能轻微闪烁');
    }

    this.camera = new THREE.PerspectiveCamera(42, 1, 0.05, 100);
    this.camera.position.set(1.9, 1.7, 3.9);

    this.controls = new OrbitControls(this.camera, canvas);
    this.controls.target.set(0.25, -0.05, -0.35);
    this.controls.enableDamping = true;
    this.controls.dampingFactor = 0.075;
    this.controls.minDistance = 0.05;
    this.controls.maxDistance = 200;
    // 旋转**永久可用**：parallaxMode 只决定"自动运动时是平移还是旋转"，不决定手能不能转。
    // 角度**不设硬限位**（用户明确要求自由旋转，转到侧面看出是纸片也接受）；
    // 只留 ±89° 俯仰防止万向锁把视角锁死。
    this.controls.minPolarAngle = Math.PI * 0.0055; // ≈ +89°
    this.controls.maxPolarAngle = Math.PI * 0.9945; // ≈ -89°
    this.controls.autoRotate = false; // 自动运动由我们的「摆动」接管，不用 autoRotate
    this.controls.update();

    // 用户一按下鼠标就关掉自动运动（否则自动 pose 每帧覆盖相机，手和自动运动会打架）。
    // ★ 不能依赖 OrbitControls 的 'start' 事件：旋转被禁用/阻尼态下它不一定派发，
    //   实测会退化成"能缩放、但拖拽完全没反应"。直接在 canvas 上监听 pointerdown。
    const stopAutoMotion = (): void => {
      if (!this.params.autoSway) return;
      this.params.autoSway = false;
      this.onInternalParamChange?.();
    };
    canvas.addEventListener('pointerdown', stopAutoMotion);
    canvas.addEventListener('wheel', stopAutoMotion, { passive: true });

    // ── uniforms（先建对象，material 和 applyParams 共用同一份引用）──
    this.pointUniforms = {
      uPointSize: { value: this.params.pointSize },
      uScale: { value: REF_DEPTH_DEFAULT },
      uMinSize: { value: 1.0 },
      uMaxSize: { value: 255.0 },
      uFogNear: { value: DEPTH_NEAR_DEFAULT },
      uFogFar: { value: DEPTH_FAR_DEFAULT },
      uDensity: { value: this.params.density },
      uPaper: { value: this.paperColor },
      uInk: { value: this.inkColor },
      uSoftness: { value: this.params.softness },
      uStipple: { value: this.params.stipple },
      uDensityGamma: { value: this.params.densityGamma },
      uMono: { value: this.params.mono },
      uFogAmount: { value: this.params.fogAmount },
      uBoilSeed: { value: 0 },
      uToneLow: { value: 0 },
      uToneHigh: { value: 1 },
    };

    this.depthUniforms = {
      uPointSize: this.pointUniforms.uPointSize,
      uScale: this.pointUniforms.uScale,
      uGrow: { value: DEPTH_POINT_GROW },
      uDepthNear: { value: DEPTH_NEAR_DEFAULT },
      uDepthFar: { value: DEPTH_FAR_DEFAULT },
    };

    this.smoothUniforms = {
      tDepth: { value: null },
      uTexel: { value: new THREE.Vector2(1, 1) },
      uRangeTol: { value: 0.02 },
    };

    this.compositeUniforms = {
      tColor: { value: null },
      tDepth: { value: null },
      tNoise: { value: blueNoise },
      uTexel: { value: new THREE.Vector2(1, 1) },
      uEdgeStrength: { value: this.params.edgeStrength },
      uEdgeThreshold: { value: this.params.edgeThreshold },
      uEdgeWidth: { value: this.params.edgeWidth },
      uInk: { value: this.inkColor },
      uVignette: { value: this.params.vignette },
      uGrain: { value: this.params.grain },
      uBoilJitter: { value: new THREE.Vector2() },
      uBoilFlicker: { value: 0 },
      uColorMode: { value: this.params.colorMode },
      uColorPosterize: { value: this.params.colorPosterize },
      uColorHalftone: { value: this.params.colorHalftone },
      uColorSaturation: { value: this.params.colorSaturation },
      uColorCell: { value: this.params.halftoneCell },
      uColorAngle: { value: THREE.MathUtils.degToRad(this.params.halftoneAngle) },
      tPaperNoise: { value: blueNoise },
    };

    this.pointMaterial = new THREE.ShaderMaterial({
      name: 'stipple-print/points',
      uniforms: this.pointUniforms as unknown as Record<string, THREE.IUniform>,
      vertexShader: pointsVert,
      fragmentShader: stippleFrag,
      transparent: true,
      blending: THREE.NormalBlending,
      depthTest: true,
      depthWrite: true,
    });

    this.depthMaterial = new THREE.ShaderMaterial({
      name: 'stipple-print/depth',
      uniforms: this.depthUniforms as unknown as Record<string, THREE.IUniform>,
      vertexShader: depthVert,
      fragmentShader: depthFrag,
      transparent: false,
      blending: THREE.NoBlending,
      depthTest: true,
      depthWrite: true,
    });

    this.smoothMaterial = new THREE.ShaderMaterial({
      name: 'stipple-print/smooth-depth',
      uniforms: this.smoothUniforms as unknown as Record<string, THREE.IUniform>,
      vertexShader: quadVert,
      fragmentShader: smoothFrag,
      depthTest: false,
      depthWrite: false,
    });

    this.compositeMaterial = new THREE.ShaderMaterial({
      name: 'stipple-print/composite',
      uniforms: this.compositeUniforms as unknown as Record<string, THREE.IUniform>,
      vertexShader: quadVert,
      fragmentShader: postEdgeFrag,
      depthTest: false,
      depthWrite: false,
    });

    // 半调漫画的合成 pass：同一份 tColor/tDepth，多做一次屏幕空间网点化
    this.inkUniforms = {
      uInkSource: { value: this.params.inkSource },
      uInkPolarity: { value: this.params.inkPolarity },
      uInkGain: { value: this.params.inkGain },
      uMeanLuminance: { value: 0.5 },
    };
    this.halftoneUniforms = {
      ...this.compositeUniforms,
      uPaper: this.pointUniforms.uPaper,
      uHalftoneStrength: { value: this.params.halftone },
      uHalftoneCell: { value: this.params.halftoneCell },
      uHalftoneAngle: { value: THREE.MathUtils.degToRad(this.params.halftoneAngle) },
      uDensityGamma: { value: this.params.densityGamma },
      uHalftoneOffset: { value: new THREE.Vector2() },
      uToneRange: { value: new THREE.Vector2(0, 1) },
      ...this.inkUniforms,
    };
    this.halftoneMaterial = new THREE.ShaderMaterial({
      name: 'halftone-manga/composite',
      uniforms: this.halftoneUniforms as unknown as Record<string, THREE.IUniform>,
      vertexShader: quadVert,
      fragmentShader: halftoneFrag,
      depthTest: false,
      depthWrite: false,
    });

    // 铜版画的合成 pass：同一份 tColor/tDepth，换成排线 + posterize + duotone
    this.engravingUniforms = {
      ...this.compositeUniforms,
      uPaper: this.pointUniforms.uPaper,
      uEngravingStrength: { value: this.params.engraving },
      uHatchLayers: { value: this.params.hatchLayers },
      uHatchSpacing: { value: this.params.hatchSpacing },
      uHatchAngle: { value: THREE.MathUtils.degToRad(this.params.hatchAngle) },
      uPosterizeLevels: { value: this.params.posterizeLevels },
      uDensityGamma: { value: this.params.densityGamma },
      uScreenOffset: { value: new THREE.Vector2() },
      uToneRange: { value: new THREE.Vector2(0, 1) },
      ...this.inkUniforms,
    };
    this.engravingMaterial = new THREE.ShaderMaterial({
      name: 'engraving/composite',
      uniforms: this.engravingUniforms as unknown as Record<string, THREE.IUniform>,
      vertexShader: quadVert,
      fragmentShader: engravingFrag,
      depthTest: false,
      depthWrite: false,
    });

    // ── 图像模式（不碰深度）：原图 quad + 图像合成 pass ─────────────────────
    this.imageQuadUniforms = {
      tImage: { value: null },
      uImageScale: { value: this.imageScale },
      uPaper: this.pointUniforms.uPaper,
    };
    this.imageQuadMaterial = new THREE.ShaderMaterial({
      name: 'image/quad',
      uniforms: this.imageQuadUniforms as unknown as Record<string, THREE.IUniform>,
      vertexShader: quadVert,
      fragmentShader: imageQuadFrag,
      depthTest: false,
      depthWrite: false,
    });

    this.imagePrintUniforms = {
      tColor: { value: null },
      tNoise: { value: blueNoise },
      uTexel: { value: new THREE.Vector2(1, 1) },
      uPaper: this.pointUniforms.uPaper,
      uInk: this.pointUniforms.uInk,
      uStyle: { value: 0 },
      uMono: { value: 1 },
      uDensityGamma: { value: this.params.densityGamma },
      uStippleStrength: { value: this.params.stipple },
      uStippleScale: { value: this.params.stippleScale },
      uHalftoneStrength: { value: this.params.halftone },
      uHalftoneCell: { value: this.params.halftoneCell },
      uHalftoneAngle: { value: THREE.MathUtils.degToRad(this.params.halftoneAngle) },
      uHatchStrength: { value: this.params.engraving },
      uHatchLayers: { value: this.params.hatchLayers },
      uHatchSpacing: { value: this.params.hatchSpacing },
      uHatchAngle: { value: THREE.MathUtils.degToRad(this.params.hatchAngle) },
      uScreenOffset: { value: new THREE.Vector2() },
      uInkSource: { value: this.params.inkSource },
      uInkPolarity: { value: this.params.inkPolarity },
      uInkGain: { value: this.params.inkGain },
      uMeanLuminance: { value: 0.5 },
      uEdgeStrength: { value: this.params.edgeStrength },
      uEdgeThreshold: { value: this.params.edgeThreshold },
      uEdgeScale: { value: IMAGE_EDGE_THRESHOLD_SCALE },
      uEdgeWidth: { value: this.params.edgeWidth },
      uBoilJitter: { value: new THREE.Vector2() },
      uBoilFlicker: { value: 0 },
      uVignette: { value: this.params.vignette },
      uGrain: { value: this.params.grain },
    };
    this.imagePrintMaterial = new THREE.ShaderMaterial({
      name: 'image/print',
      uniforms: this.imagePrintUniforms,
      vertexShader: quadVert,
      fragmentShader: imagePrintFrag,
      depthTest: false,
      depthWrite: false,
    });

    this.quadMesh = new THREE.Mesh(this.quadGeometry, this.smoothMaterial);
    this.quadMesh.frustumCulled = false;
    this.quadScene.add(this.quadMesh);

    this.resize();
    this.applyParams();
    window.addEventListener('resize', this.resize);
  }

  // ── 数据 ────────────────────────────────────────────────────────────────
  /**
   * 挂上点云（BufferGeometry 零拷贝引用 ILC1 的视图），并按包围盒自适应取景。
   *
   * - `preserveOrientation`：重新生成点云时保留用户**当前的旋转角度**（不强制回正）。
   * - `preserveDistance`：**连相机距离也保留**。
   *   焦距（focal_factor）只放大点云的 x/y，z 不变；如果按新包围盒重新取景，
   *   x/y 放大多少相机就退多远，正好抵消 → 焦距看起来"改了没反应"。
   *   保留距离后：焦距调小 → 点云明显变大/变宽；调大 → 变小更紧凑（这才是"镜头"的手感）。
   *   想重新适配画面时点「回正」——resetView 用 frameInfo.distance 重新取景。
   */
  setCloud(
    cloud: IlcCloud,
    source?: string,
    options: { preserveOrientation?: boolean; preserveDistance?: boolean } = {},
  ): void {
    if (cloud.count <= 0) throw new Error('点云是空的（count = 0）');

    // 记住当前视线方向（相机相对 pivot 的单位向量），取景后复原
    const previousDirection = options.preserveOrientation
      ? this.camera.position.clone().sub(this.controls.target).normalize()
      : null;
    // 旧距离：preserveDistance 时用来顶掉"按新包围盒重新适配"的那个距离
    const previousDistance = options.preserveDistance
      ? this.camera.position.distanceTo(this.controls.target)
      : null;

    if (this.colorPoints) {
      this.scene.remove(this.colorPoints);
      this.colorPoints.geometry.dispose();
    }
    if (this.depthPoints) {
      this.scene.remove(this.depthPoints);
    }

    const geometry = new THREE.BufferGeometry();
    const position = new THREE.BufferAttribute(cloud.positions, 3);
    geometry.setAttribute('position', position);
    // u8 归一化属性：shader 里 aColor 直接是 0..1
    geometry.setAttribute('aColor', new THREE.BufferAttribute(cloud.colors, 3, true));
    // 每点随机数（与位置无关）：点画抽稀/密度抽稀都用它，避免规则网格的摩尔纹
    geometry.setAttribute('aRandom', new THREE.BufferAttribute(createPointRandoms(cloud.count), 1));
    geometry.computeBoundingBox();
    geometry.computeBoundingSphere();

    this.colorPoints = new THREE.Points(geometry, this.pointMaterial);
    this.depthPoints = new THREE.Points(geometry, this.depthMaterial);
    this.depthPoints.visible = false;

    this.scene.add(this.colorPoints, this.depthPoints);

    this.frameCloud(geometry, position);

    // 重新生成时保留旋转角度（和可选的旧距离）：沿原视线方向重新摆相机
    if (previousDirection || previousDistance !== null) {
      const direction =
        previousDirection ?? this.camera.position.clone().sub(this.controls.target).normalize();
      const distance = previousDistance ?? (this.frameInfo?.distance ?? 1);
      // 焦距变小后包围盒会很大，frameCloud 设的 minDistance 可能 > 旧距离，
      // controls.update() 会把相机硬拉远（= 又把"变大"抵消掉）→ 这里先放宽范围
      if (previousDistance !== null) {
        this.controls.minDistance = Math.min(this.controls.minDistance, previousDistance * 0.5);
        this.controls.maxDistance = Math.max(this.controls.maxDistance, previousDistance * 2);
      }
      this.camera.position.copy(this.controls.target).addScaledVector(direction, distance);
      this.camera.lookAt(this.controls.target);
      this.controls.update();
      this.captureParallaxBase();
      this.renderFrame();
    }

    if (source !== undefined) this.onSource?.(source);
  }

  /**
   * 按点云的包围盒做自适应取景 + 自适应尺度参数。
   *
   * 真实点云（单图深度估计）的尺度、位置、朝向、点数都和测试点云不一样，
   * 所以这些量一个都不能写死：
   *   - 相机距离 → 用**实际投影点精确套框**（见 fitDistanceFromGeometry）：插画的边缘就是构图，
   *     必须整幅都进画面。早期用的「75 分位核心半径」是为 fixture 的地面长尾设计的，
   *     在真实插画上会过度放大（只看到局部，实测 198k 点那版就是这样）
   *   - uPointSize 的参考视距 refDepth → 跟着取景距离走，换尺度后点的屏幕大小不变
   *   - 空气透视 / 描边归一化的深度区间 → 同上
   *   - 点大小 → 由估算的「屏幕点间距」决定，点数变少时点自动变大，覆盖率不塌
   */
  private frameCloud(
    geometry: THREE.BufferGeometry,
    position: THREE.BufferAttribute,
    options: { preservePointSize?: boolean; preserveDistance?: number } = {},
  ): void {
    this.cloudGeometry = geometry;
    this.cloudPosition = position;

    const count = position.count;
    const box = geometry.boundingBox ?? new THREE.Box3().setFromBufferAttribute(position);
    const boxCenter = box.getCenter(new THREE.Vector3());
    const size = box.getSize(new THREE.Vector3());
    const diagonal = Math.max(size.length(), 1e-6);

    // 旋转中心（pivot）= 主体**前表面**，不是包围盒中心。
    // 真实插画的深度是「主体板 + 背景板」，包围盒中心的 z 落在两层之间的空处 →
    // 绕空中一点旋转，观感很怪（点云像被甩出去）。取画面中心 35% 区域内点的 z 中位数，
    // 基本就是主体板所在的深度。
    const center = this.computePivot(position, boxCenter);

    // 核心半径（给 OrbitControls 的缩放范围与视差幅度当"场景尺度"用）
    const radius = Math.max(this.percentileRadius(position, center, FRAME_RADIUS_PERCENTILE), 1e-6);

    const fovRad = THREE.MathUtils.degToRad(this.camera.fov);

    // 基准方向：正面（默认）或 3/4（调试开关）
    this.threeQuarterView = this.params.threeQuarterView;
    const dir = (
      this.threeQuarterView ? BASE_DIR_THREE_QUARTER : BASE_DIR_FRONT
    ).clone().normalize();
    this.controls.target.copy(center);

    // 取景基准位姿：摆动与角度限位都围绕它
    this.baseAzimuth = Math.atan2(dir.x, dir.z);
    this.baseElevation = Math.asin(dir.y);

    // 相机距离：包围盒精确套框（整幅插画进画面），留 FRAME_MARGIN 边
    const fitDistance = this.fitDistanceFromGeometry(position, center, dir, fovRad, diagonal);

    this.camera.near = Math.max(fitDistance * 0.005, 1e-5);
    this.camera.far = fitDistance * 12 + diagonal * 2;
    this.camera.updateProjectionMatrix();

    this.controls.minDistance = radius * 0.25;
    this.controls.maxDistance = fitDistance * 6;
    this.poseFromOffsets(0, 0, options.preserveDistance ?? fitDistance);
    this.applySwayLimits();
    this.controls.update();
    // 记录视差基准位姿（位置 + 朝向）；场景宽度供视差幅度换算
    this.sceneWidth = Math.max(radius * 2, 1e-6);
    this.captureParallaxBase();

    this.frameInfo = {
      center: center.clone(),
      radius,
      distance: options.preserveDistance ?? fitDistance,
    };

    // 尺度相关参数：换尺度后观感保持一致
    this.refDepth = (options.preserveDistance ?? fitDistance) * 0.91;

    // 深度区间：用**包围盒沿视线的真实深度跨度**，而不是取景距离的比例。
    // 用取景距离的比例会在真实插画上把整个深度跨度压到 0.1 以内：
    //   - 空气透视（雾）几乎不动 → 远近没差别
    //   - 描边归一化的梯度尺度也失真（阈值语义随数据漂移）
    const viewDepth = this.viewDepthRange(box, center, dir, options.preserveDistance ?? fitDistance);
    this.depthNear = viewDepth.near;
    this.depthFar = viewDepth.far;
    this.applyDepthRange();

    // 点大小自适应：点云来自深度图，基本是 2.5D 面 → 间距 ≈ 对角线 / sqrt(点数)
    if (!options.preservePointSize) {
      const spacingWorld = diagonal / Math.sqrt(count);
      const cssHeight = Math.max(window.innerHeight, 1);
      const pxPerWorldAtRef = (cssHeight * 0.5) / Math.tan(fovRad / 2) / this.refDepth;
      const spacingPx = spacingWorld * pxPerWorldAtRef;
      this.params.pointSize = THREE.MathUtils.clamp(
        spacingPx * POINT_SIZE_PER_SPACING,
        POINT_SIZE_MIN,
        POINT_SIZE_MAX,
      );
    }
    this.computeToneRange(geometry);
    this.applyParams();
  }

  /**
   * 自动色调范围（auto-levels）：点云亮度的 5~95 分位。
   *
   * 真实插画经常整片偏亮（白底/浅色角色），若直接用绝对亮度做「暗处点密」，
   * 亮部会全部变成空白纸、画面散架。取分位数归一化后，疏密对比才落在真实动态范围上。
   * 这是印刷里的 auto-levels，不是为某张图调参。
   */
  private computeToneRange(geometry: THREE.BufferGeometry): void {
    const attribute = geometry.getAttribute('aColor') as THREE.BufferAttribute | undefined;
    if (!attribute) {
      this.toneLow = 0;
      this.toneHigh = 1;
      return;
    }
    const array = attribute.array as ArrayLike<number>;
    const scale = array instanceof Uint8Array ? 1 / 255 : 1;
    const count = attribute.count;
    const stride = Math.max(1, Math.floor(count / 8000));
    const lumas: number[] = [];
    for (let i = 0; i < count; i += stride) {
      const r = (array[i * 3] ?? 0) * scale;
      const g = (array[i * 3 + 1] ?? 0) * scale;
      const b = (array[i * 3 + 2] ?? 0) * scale;
      lumas.push(0.299 * r + 0.587 * g + 0.114 * b);
    }
    if (lumas.length === 0) {
      this.toneLow = 0;
      this.toneHigh = 1;
      return;
    }
    lumas.sort((a, b) => a - b);
    const low = lumas[Math.floor(lumas.length * 0.05)] as number;
    const high = lumas[Math.min(lumas.length - 1, Math.floor(lumas.length * 0.95))] as number;
    const span = Math.max(high - low, 0.08); // 纯色点云也要留一点跨度，避免除零
    this.toneLow = Math.max(0, low - span * 0.05);
    this.toneHigh = Math.min(1, high + span * 0.05);
  }

  /** 包围盒沿视线方向的深度跨度（世界单位），两侧留一点边 */
  private viewDepthRange(
    box: THREE.Box3,
    center: THREE.Vector3,
    dir: THREE.Vector3,
    distance: number,
  ): { near: number; far: number } {
    const viewDir = dir.clone().negate();
    const corner = new THREE.Vector3();
    let minAlong = Infinity;
    let maxAlong = -Infinity;
    for (let ix = 0; ix < 2; ix++) {
      for (let iy = 0; iy < 2; iy++) {
        for (let iz = 0; iz < 2; iz++) {
          corner.set(
            ix ? box.max.x : box.min.x,
            iy ? box.max.y : box.min.y,
            iz ? box.max.z : box.min.z,
          );
          const along = corner.clone().sub(center).dot(viewDir);
          if (along < minAlong) minAlong = along;
          if (along > maxAlong) maxAlong = along;
        }
      }
    }
    const span = Math.max(maxAlong - minAlong, Math.max(distance * 0.02, 1e-4));
    const pad = Math.max(span * 0.06, 1e-4);
    return {
      near: Math.max(distance + minAlong - pad, 1e-3),
      far: distance + maxAlong + pad,
    };
  }

  /**
   * 相机的「精确套框」距离：让**实际点**都落在视锥内（留 FRAME_MARGIN 边）。
   *
   * 相机在 `center + dir·D`，看向 center。对每个点 p：
   *   a = (p - center)·(-dir)      // 沿视线的深度偏移（正 = 更远）
   *   x = |(p - center)·right|, y = |(p - center)·up|
   *   需要 x ≤ tanH·(D + a), y ≤ tanV·(D + a)
   *   ⇒ D ≥ max(x/tanH, y/tanV) - a
   *
   * 用实际点而不是包围盒 8 个角：真实深度点云是「主体板 + 背景板」，
   * 包围盒的角往往是空的（z 极值处的 x/y 并不存在），按角点套框会算得太近。
   * 抽样 4000 个点 + 用分位数（0.2%~99.8%）抗离群点。
   */
  private fitDistanceFromPoints(
    position: THREE.BufferAttribute,
    center: THREE.Vector3,
    dir: THREE.Vector3,
    fovRad: number,
  ): number {
    const count = position.count;
    const stride = Math.max(1, Math.floor(count / 4000));
    const viewDir = dir.clone().negate();
    const right = new THREE.Vector3().crossVectors(new THREE.Vector3(0, 1, 0), dir);
    if (right.lengthSq() < 1e-6) right.set(1, 0, 0);
    right.normalize();
    const up = new THREE.Vector3().crossVectors(dir, right).normalize();

    const tanV = Math.tan(fovRad / 2);
    const aspect = this.camera.aspect > 0 ? this.camera.aspect : 1;
    const tanH = tanV * aspect;

    const needed: number[] = [];
    for (let i = 0; i < count; i += stride) {
      const dx = position.getX(i) - center.x;
      const dy = position.getY(i) - center.y;
      const dz = position.getZ(i) - center.z;
      const along = dx * viewDir.x + dy * viewDir.y + dz * viewDir.z;
      const x = Math.abs(dx * right.x + dy * right.y + dz * right.z);
      const y = Math.abs(dx * up.x + dy * up.y + dz * up.z);
      needed.push(Math.max(x / tanH, y / tanV) - along);
    }
    if (needed.length === 0) return 1;
    needed.sort((a, b) => a - b);
    const index = Math.min(needed.length - 1, Math.floor(needed.length * 0.998));
    const picked = Math.max(needed[index] as number, 1e-6);
    return picked * FRAME_MARGIN;
  }

  /**
   * 相机距离：按实际点套框（整幅插画进画面）。
   *
   * 早期用的是「75 分位核心半径 / sin(fov/2)」+ 包围盒套框，两者在真实插画上都会
   * 过度放大（只看到局部）。真实点云是「主体板 + 背景板」，必须按实际投影点算。
   */
  private fitDistanceFromGeometry(
    position: THREE.BufferAttribute,
    center: THREE.Vector3,
    dir: THREE.Vector3,
    fovRad: number,
    diagonal: number,
  ): number {
    return Math.max(
      this.fitDistanceFromPoints(position, center, dir, fovRad),
      diagonal * 0.2,
    );
  }

  /** 基准视角切换后重新取景（保持当前点大小与视距） */
  private reframeForBaseView(): void {
    if (!this.cloudGeometry || !this.cloudPosition) return;
    const distance = this.camera.position.distanceTo(this.controls.target);
    this.frameCloud(this.cloudGeometry, this.cloudPosition, {
      preservePointSize: true,
      preserveDistance: distance,
    });
  }

  /**
   * 设置相机 FOV（越小越接近**正交投影**）。
   *
   * 为什么需要：DA3 输出的是相对深度、没有内参，正确做法是正交反投影（x 与深度无关）。
   * 但若渲染相机是透视的，远层在画面上又被缩小一次，于是整幅图仍呈放射状。
   * 取景距离 ∝ 1/tan(fov/2)，所以 FOV 越小，跨深度的缩放假象越弱：
   *   42° → 跨 Δz 的缩放差约 2.1 倍（这就是"扇形"的来源）
   *    1° → 约 1.5%
   *
   * 改完必须重新取景：fitDistance / refDepth / uPointSize 全部依赖 fov。
   */
  setCameraFov(deg: number): void {
    this.camera.fov = THREE.MathUtils.clamp(deg, 0.2, 90);
    this.params.cameraFov = this.camera.fov;
    this.camera.updateProjectionMatrix();
    // 必须**重新套框**（不能保留旧视距）：取景距离 ∝ 1/tan(fov/2)，
    // FOV 从 42° 降到 1° 时视距要放大 ~40 倍，保留旧距离只会看到一小块。
    if (this.cloudGeometry && this.cloudPosition) {
      this.frameCloud(this.cloudGeometry, this.cloudPosition);
    }
  }

  /** 当前相机 FOV（度）。 */
  get cameraFov(): number {
    return this.camera.fov;
  }

  /**
   * 把相机放到「相对取景基准的偏移角度」上。
   * 实时摆动与录制捕获共用这一条路径，保证两者观感一致、且都能回绕。
   */
  private poseFromOffsets(azimuthOffsetRad: number, elevationOffsetRad: number, distance: number): void {
    const azimuth = this.baseAzimuth + azimuthOffsetRad;
    const elevation = THREE.MathUtils.clamp(
      this.baseElevation + elevationOffsetRad,
      THREE.MathUtils.degToRad(ELEVATION_MIN_DEG),
      THREE.MathUtils.degToRad(ELEVATION_MAX_DEG),
    );
    const cosElevation = Math.cos(elevation);
    const target = this.controls.target;
    this.camera.position.set(
      target.x + distance * cosElevation * Math.sin(azimuth),
      target.y + distance * Math.sin(elevation),
      target.z + distance * cosElevation * Math.cos(azimuth),
    );
    this.camera.lookAt(target);
    this.camera.updateMatrixWorld();
  }

  /** 摆动幅度（度）→ 弧度 */
  private swayAmplitudeRad(): number {
    return THREE.MathUtils.degToRad(
      THREE.MathUtils.clamp(this.params.swayAmplitude, 0, SWAY_MAX_DEG),
    );
  }

  // ── 图像模式 ────────────────────────────────────────────────────────────
  /**
   * 载入一张「已经是成品画面」的图（图像模式用）：不经过深度，直接用原图做风格化。
   * meanLuminance 用来判断主体极性"自动"应该按亮主体还是暗主体处理。
   */
  setImage(texture: THREE.Texture, aspect: number, meanLuminance: number): void {
    this.imageTexture?.dispose();
    this.imageTexture = texture;
    this.imageAspect = aspect > 0 ? aspect : 1;
    this.imageMeanLuminance = meanLuminance;
    this.imageQuadUniforms.tImage.value = texture;
    this.imagePrintUniforms.uMeanLuminance.value = meanLuminance;
    const size = this.renderer.getDrawingBufferSize(new THREE.Vector2());
    this.updateImageScale(size.x / Math.max(size.y, 1));
    this.renderFrame();
  }

  /** 图像模式下是否可用（有图 + 模式是 image） */
  get hasImage(): boolean {
    return this.imageTexture !== null;
  }

  /** 图像 contain 摆放：计算图像占屏幕宽/高的比例 */
  private updateImageScale(viewAspect: number): void {
    if (this.imageAspect >= viewAspect) {
      this.imageScale.set(1, viewAspect / Math.max(this.imageAspect, 1e-6));
    } else {
      this.imageScale.set(this.imageAspect / Math.max(viewAspect, 1e-6), 1);
    }
    this.imageQuadUniforms.uImageScale.value.copy(this.imageScale);
  }

  /** 当前是否走图像模式（没上传图时自动退回点云模式） */
  private useImageMode(): boolean {
    return this.params.renderMode === 'image' && this.imageTexture !== null;
  }

  /** 视差幅度（世界单位）：场景宽度 × 百分比 */
  private parallaxAmplitude(): number {
    const percent = THREE.MathUtils.clamp(this.params.parallaxAmount, 0, PARALLAX_MAX_PERCENT);
    return (percent / 100) * this.sceneWidth;
  }

  /**
   * 记录当前的「视差基准位姿」：位置 + 朝向 + 相机的右/上基向量。
   *
   * 视差模式下相机只做平移，**朝向固定** —— 这是整个交互成立的关键：
   * 平板永远平行于画面，近层/远层的屏幕位移差（视差）提供深度感。
   */
  private captureParallaxBase(): void {
    this.parallaxBasePosition.copy(this.camera.position);
    this.parallaxBaseTarget.copy(this.controls.target);
    this.parallaxBaseQuaternion.copy(this.camera.quaternion);
    this.parallaxRight.set(1, 0, 0).applyQuaternion(this.parallaxBaseQuaternion);
    this.parallaxUp.set(0, 1, 0).applyQuaternion(this.parallaxBaseQuaternion);
  }

  /**
   * 视差位姿：相机与 target 同时平移 (dx, dy)，**四元数原样保留**（朝向不变）。
   *
   * 注意不能用 `lookAt(0,0,0)`：相机平移后仍看向原点 = 又变成旋转了。
   * 看向"同样偏移的点"才等价于纯平移。
   */
  private poseParallax(dx: number, dy: number): void {
    this.camera.position
      .copy(this.parallaxBasePosition)
      .addScaledVector(this.parallaxRight, dx)
      .addScaledVector(this.parallaxUp, dy);
    this.camera.quaternion.copy(this.parallaxBaseQuaternion);
    this.controls.target
      .copy(this.parallaxBaseTarget)
      .addScaledVector(this.parallaxRight, dx)
      .addScaledVector(this.parallaxUp, dy);
    this.camera.updateMatrixWorld();
  }

  /** 手动平移后把基准位姿更新到当前（视差模式下拖动 = 平移） */
  private syncParallaxBaseFromCamera(): void {
    this.captureParallaxBase();
  }

  /**
   * 手动旋转的角度范围。
   *
   * - reeRotate = true（默认）：**不设方位限位**，俯仰只留 ±89° 防万向锁。
   *   用户明确要求自由旋转——"转到侧面看出是纸片也接受，自己会转回来"。
   * - reeRotate = false：退回旧的 ±摆动幅度 限位（幅度 0 时锁死正面）。
   */
  private applySwayLimits(): void {
    if (this.params.freeRotate) {
      this.controls.minAzimuthAngle = -Infinity;
      this.controls.maxAzimuthAngle = Infinity;
      this.controls.minPolarAngle = Math.PI * 0.0055; // ≈ +89°
      this.controls.maxPolarAngle = Math.PI * 0.9945; // ≈ -89°
      return;
    }
    const amplitude = this.swayAmplitudeRad();
    this.controls.minAzimuthAngle = this.baseAzimuth - amplitude;
    this.controls.maxAzimuthAngle = this.baseAzimuth + amplitude;
    const basePolar = Math.PI / 2 - this.baseElevation;
    const pitchAmplitude = amplitude * SWAY_PITCH_RATIO;
    this.controls.minPolarAngle = THREE.MathUtils.clamp(
      basePolar - pitchAmplitude,
      0.05,
      Math.PI - 0.05,
    );
    this.controls.maxPolarAngle = THREE.MathUtils.clamp(
      basePolar + pitchAmplitude,
      0.05,
      Math.PI - 0.05,
    );
  }

  /**
   * 把相机设到「基准 + 水平偏移角度」上（度）。
   * 给自动化截图 / 预设立场用；也方便以后接 Go 侧传来的正面朝向。
   */
  setSwingAngles(azimuthOffsetDeg: number, elevationOffsetDeg = 0): void {
    const distance = this.camera.position.distanceTo(this.controls.target);
    this.poseFromOffsets(
      THREE.MathUtils.degToRad(azimuthOffsetDeg),
      THREE.MathUtils.degToRad(elevationOffsetDeg),
      distance,
    );
    this.controls.update();
    this.renderFrame();
  }

  /**
   * 回正：把相机恢复成 `frameCloud()` 算出的基准位姿（基准方位/仰角/距离，看向 pivot）。
   * 用户要求「不要限制角度，但要能一键转回来」。
   */
  resetView(): void {
    const center = this.frameInfo?.center ?? this.controls.target.clone();
    const distance =
      this.frameInfo?.distance ?? Math.max(this.camera.position.distanceTo(this.controls.target), 1e-3);
    this.controls.target.copy(center);
    this.poseFromOffsets(0, 0, distance);
    this.controls.update();
    this.captureParallaxBase();
    // 回正后如果自动运动还开着，让它从基准姿态继续
    this.lastTickAt = 0;
    this.renderFrame();
  }

  /**
   * 视差模式：直接设定当前横移量（场景宽度百分比）。给自动化截图 / 预设立场用。
   * 内部走 poseParallax → 朝向不变。
   */
  setParallaxOffset(percent: number): void {
    if (!this.params.parallaxMode) {
      this.params.parallaxMode = true;
      this.setParams(this.params);
    }
    const world = (percent / 100) * this.sceneWidth;
    this.poseParallax(world, 0.5 * world);
    this.renderFrame();
  }

  /** 当前视差横移量（场景宽度百分比） */
  getParallaxOffset(): number {
    const dx = this.camera.position.distanceTo(this.parallaxBasePosition);
    return this.sceneWidth > 0 ? (dx / this.sceneWidth) * 100 : 0;
  }

  /** 场景宽度（= 2×核心半径），视差幅度的基准 */
  get sceneWidthValue(): number {
    return this.sceneWidth;
  }

  /** 当前相机相对取景基准的方位/俯仰偏移（度），给自检/统计用 */
  getSwingAngles(): { azimuthDeg: number; elevationDeg: number } {
    const offset = this.camera.position.clone().sub(this.controls.target);
    const distance = Math.max(offset.length(), 1e-6);
    const azimuth = Math.atan2(offset.x, offset.z);
    const elevation = Math.asin(THREE.MathUtils.clamp(offset.y / distance, -1, 1));
    return {
      azimuthDeg: THREE.MathUtils.radToDeg(azimuth - this.baseAzimuth),
      elevationDeg: THREE.MathUtils.radToDeg(elevation - this.baseElevation),
    };
  }

  /**
   * 旋转中心（pivot）：`(bbox 中心 x/y, 画面中心区域点的 z 中位数)`。
   *
   * 为什么要动 z：点云是「主体板 + 背景板」两层，bbox 中心的 z 在两层之间的空中，
   * 绕那里旋转会让主体像被甩出去。画面中心 35% 半径内的点近似主体，取它们 z 的中位数
   * 就落在主体前表面上。中心区域点太少时退回全点云 z 中位数。
   *
   * 画面半径用 rho = hypot(x, y) / |z| 衡量（与深度无关，等价于图像空间半径 / 焦距）。
   */
  private computePivot(position: THREE.BufferAttribute, boxCenter: THREE.Vector3): THREE.Vector3 {
    const count = position.count;
    const stride = Math.max(1, Math.floor(count / 20000));
    const rhos: number[] = [];
    const samples: Array<{ rho: number; z: number }> = [];
    for (let i = 0; i < count; i += stride) {
      const x = position.getX(i);
      const y = position.getY(i);
      const z = position.getZ(i);
      const rho = Math.hypot(x, y) / Math.max(Math.abs(z), 1e-6);
      rhos.push(rho);
      samples.push({ rho, z });
    }
    if (samples.length === 0) return boxCenter.clone();

    const median = (values: number[]): number => {
      const sorted = [...values].sort((a, b) => a - b);
      return sorted[Math.floor(sorted.length / 2)] as number;
    };
    const sortedRhos = [...rhos].sort((a, b) => a - b);
    const rho95 = sortedRhos[Math.min(sortedRhos.length - 1, Math.floor(sortedRhos.length * 0.95))] as number;

    const inner = samples.filter((s) => s.rho < rho95 * 0.35);
    const zSource =
      inner.length >= Math.max(50, samples.length * 0.02)
        ? inner.map((s) => s.z)
        : samples.map((s) => s.z);
    const pivot = boxCenter.clone();
    pivot.z = median(zSource);
    return pivot;
  }

  /** 到 center 距离的第 p 分位（抽样计算，够用且快） */
  private percentileRadius(
    position: THREE.BufferAttribute,
    center: THREE.Vector3,
    p: number,
  ): number {
    const count = position.count;
    const stride = Math.max(1, Math.floor(count / 20000));
    const samples: number[] = [];
    for (let i = 0; i < count; i += stride) {
      const dx = position.getX(i) - center.x;
      const dy = position.getY(i) - center.y;
      const dz = position.getZ(i) - center.z;
      samples.push(Math.sqrt(dx * dx + dy * dy + dz * dz));
    }
    if (samples.length === 0) return 1;
    samples.sort((a, b) => a - b);
    const index = Math.min(samples.length - 1, Math.max(0, Math.floor(samples.length * p)));
    return samples[index] as number;
  }

  /** 把 refDepth / 深度区间刷到 uniform 上（dpr 变化时也要调） */
  private applyDepthRange(): void {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.pointUniforms.uScale.value = this.refDepth * dpr;
    this.pointUniforms.uFogNear.value = this.depthNear;
    this.pointUniforms.uFogFar.value = this.depthFar;
    this.depthUniforms.uDepthNear.value = this.depthNear;
    this.depthUniforms.uDepthFar.value = this.depthFar;
  }

  /** 当前取景信息（HUD / 自动化用） */
  get framing(): { center: THREE.Vector3; radius: number; distance: number } | null {
    return this.frameInfo;
  }

  // ── 参数 ────────────────────────────────────────────────────────────────
  /** 应用一整套参数（面板 / 预设都走这里） */
  setParams(next: RenderParams): void {
    Object.assign(this.params, next);
    this.applyParams();
  }

  /**
   * 应用某个预设（画风）。
   * 预设只决定「画风 / 墨量语义」，不决定**「要不要立体」**——所以保留当前的 renderMode：
   * 在图像模式里点预设不该被踢回点云模式（这一层是独立的）。
   */
  applyPreset(key: PresetKey): void {
    const renderMode = this.params.renderMode;
    const inkSource = this.params.inkSource;
    const inkPolarity = this.params.inkPolarity;
    const inkGain = this.params.inkGain;
    const next = cloneParams(PRESETS[key].params);
    next.renderMode = renderMode;
    next.inkSource = inkSource;
    next.inkPolarity = inkPolarity;
    next.inkGain = inkGain;
    this.setParams(next);
  }

  private applyParams(): void {
    const p = this.params;

    // 相机 FOV（越小越接近正交）：只在真的变了才重新套框，
    // 否则拖任何滑块都会把用户转到的视角重置回正面。
    if (Math.abs(this.camera.fov - p.cameraFov) > 1e-3) {
      this.setCameraFov(p.cameraFov);
    }

    this.paperColor.set(p.paper);
    // 画布底色未显式设置时沿用纸色（保证预设观感不变）
    this.canvasBgColor.set(p.bgColor ?? p.paper);
    this.pointUniforms.uPaper.value = this.paperColor;
    this.inkColor.set(p.ink);

    this.pointUniforms.uPointSize.value = p.pointSize;
    this.pointUniforms.uSoftness.value = p.softness;
    this.pointUniforms.uDensity.value = p.density;
    this.pointUniforms.uStipple.value = p.stipple;
    this.pointUniforms.uDensityGamma.value = p.densityGamma;
    this.pointUniforms.uMono.value = p.mono;
    this.pointUniforms.uFogAmount.value = p.fogAmount;
    this.pointUniforms.uToneLow.value = this.toneLow;
    this.pointUniforms.uToneHigh.value = this.toneHigh;
    this.halftoneUniforms.uToneRange.value.set(this.toneLow, this.toneHigh);
    // ── 彩色点画（保留原色）：点只带颜色，印刷质感在合成 pass 叠 ──────────
    const colorMode = p.colorMode > 0.5;
    if (colorMode) {
      // 关键：不再是"亮度决定点密度"。所有点都画（密度滑块仍然均匀抽稀）。
      this.pointUniforms.uStipple.value = 0;
      this.pointUniforms.uMono.value = 0;
      // 彩色点的明暗/层次由颜色本身表达：空气透视会把颜色洗成纸色，彩色路径关掉。
      this.pointUniforms.uFogAmount.value = 0;
      // 线稿已经在每点颜色里，深度描边只留一点点（否则 silhouette 一圈粗黑边抢戏）。
      this.compositeUniforms.uEdgeStrength.value = p.edgeStrength * 0.05;
      // 彩色点要更"实"：点略放大盖住纸缝，颜色才不会被纸色稀释成粉彩。
      this.pointUniforms.uPointSize.value = p.pointSize * 1.35;
    }
    this.compositeUniforms.uColorMode.value = colorMode ? 1 : 0;
    this.compositeUniforms.uColorPosterize.value = p.colorPosterize;
    this.compositeUniforms.uColorHalftone.value = p.colorHalftone;
    this.compositeUniforms.uColorSaturation.value = p.colorSaturation;
    this.compositeUniforms.uColorCell.value = p.halftoneCell;
    this.compositeUniforms.uColorAngle.value = THREE.MathUtils.degToRad(p.halftoneAngle);

    this.halftoneUniforms.uInkSource.value = p.inkSource;
    this.halftoneUniforms.uInkPolarity.value = p.inkPolarity;
    this.halftoneUniforms.uInkGain.value = p.inkGain;
    this.halftoneUniforms.uMeanLuminance.value = this.imageMeanLuminance;
    this.engravingUniforms.uToneRange.value.set(this.toneLow, this.toneHigh);
    this.engravingUniforms.uInkSource.value = p.inkSource;
    this.engravingUniforms.uInkPolarity.value = p.inkPolarity;
    this.engravingUniforms.uInkGain.value = p.inkGain;
    this.engravingUniforms.uMeanLuminance.value = this.imageMeanLuminance;

    this.compositeUniforms.uEdgeStrength.value = p.edgeStrength;
    this.compositeUniforms.uEdgeThreshold.value = p.edgeThreshold;
    this.compositeUniforms.uEdgeWidth.value = p.edgeWidth;
    this.compositeUniforms.uVignette.value = p.vignette;
    this.compositeUniforms.uGrain.value = p.grain;

    // 半调漫画（网点在合成 pass 里做）
    this.halftoneUniforms.uHalftoneStrength.value = p.halftone;
    this.halftoneUniforms.uHalftoneCell.value = p.halftoneCell;
    this.halftoneUniforms.uHalftoneAngle.value = THREE.MathUtils.degToRad(p.halftoneAngle);
    this.halftoneUniforms.uDensityGamma.value = p.densityGamma;

    // 铜版画（排线在合成 pass 里做）
    this.engravingUniforms.uEngravingStrength.value = p.engraving;
    this.engravingUniforms.uHatchLayers.value = p.hatchLayers;
    this.engravingUniforms.uHatchSpacing.value = p.hatchSpacing;
    this.engravingUniforms.uHatchAngle.value = THREE.MathUtils.degToRad(p.hatchAngle);
    this.engravingUniforms.uPosterizeLevels.value = p.posterizeLevels;
    this.engravingUniforms.uDensityGamma.value = p.densityGamma;

    // ── 图像模式：同一套墨/纸外观参数 ──────────────────────────────────────
    const image = this.imagePrintUniforms;
    image.uStyle.value = p.engraving > 0.001 ? 2 : p.halftone > 0.001 ? 1 : 0;
    image.uMono.value = p.mono;
    image.uDensityGamma.value = p.densityGamma;
    image.uStippleStrength.value = p.stipple;
    image.uStippleScale.value = p.stippleScale;
    image.uHalftoneStrength.value = p.halftone;
    image.uHalftoneCell.value = p.halftoneCell;
    image.uHalftoneAngle.value = THREE.MathUtils.degToRad(p.halftoneAngle);
    image.uHatchStrength.value = p.engraving;
    image.uHatchLayers.value = p.hatchLayers;
    image.uHatchSpacing.value = p.hatchSpacing;
    image.uHatchAngle.value = THREE.MathUtils.degToRad(p.hatchAngle);
    image.uInkSource.value = p.inkSource;
    image.uInkPolarity.value = p.inkPolarity;
    image.uInkGain.value = p.inkGain;
    image.uEdgeStrength.value = p.edgeStrength;
    image.uEdgeThreshold.value = p.edgeThreshold;
    image.uEdgeWidth.value = p.edgeWidth;
    image.uVignette.value = p.vignette;
    image.uGrain.value = p.grain;

    // 摆动幅度变了要重设角度限位（0 = 锁死正面）
    this.applySwayLimits();

    // 旋转**永久可用**（parallaxMode 只决定自动运动的形态，不决定手能否转）
    this.controls.enableRotate = true;
    if (p.parallaxMode !== this.parallaxMode) {
      this.parallaxMode = p.parallaxMode;
      if (p.parallaxMode) {
        // 进入视差：让相机回到取景基准（避免从倾斜姿态开始平移）
        const distance = this.camera.position.distanceTo(this.controls.target);
        this.poseFromOffsets(0, 0, distance);
        this.controls.update();
        this.captureParallaxBase();
      } else {
        this.captureParallaxBase();
      }
    }

    // 基准视角切换（正面 ↔ 3/4）需要重新取景
    if (p.threeQuarterView !== this.threeQuarterView) {
      this.reframeForBaseView();
    }
  }

  // ── 尺寸 ────────────────────────────────────────────────────────────────
  private readonly resize = (): void => {
    if (this.disposed) return;
    // 录制时用输出尺寸覆盖窗口尺寸，pixelRatio 固定 1 → 绘图缓冲就是目标像素数
    const override = this.exportSize;
    const width = override?.width ?? window.innerWidth;
    const height = override?.height ?? window.innerHeight;
    const dpr = override ? 1 : Math.min(window.devicePixelRatio || 1, 2);

    this.renderer.setPixelRatio(dpr);
    this.renderer.setSize(width, height, false);
    this.camera.aspect = width / Math.max(height, 1);
    this.camera.updateProjectionMatrix();

    const drawing = this.renderer.getDrawingBufferSize(new THREE.Vector2());
    const w = Math.max(1, Math.floor(drawing.x));
    const h = Math.max(1, Math.floor(drawing.y));

    this.colorTarget?.dispose();
    this.depthTarget?.dispose();
    this.smoothTarget?.dispose();

    // 颜色：8 位够了（最终也是 8 位上屏），但需要 depth buffer 做点云遮挡
    this.colorTarget = new THREE.WebGLRenderTarget(w, h, {
      minFilter: THREE.LinearFilter,
      magFilter: THREE.LinearFilter,
      type: THREE.UnsignedByteType,
      depthBuffer: true,
      stencilBuffer: false,
      generateMipmaps: false,
    });

    // 深度 pass：**必须有 depth buffer**。
    // 点云没有绘制顺序保证（ILC1 里是 地面→球→立方体，立方体内部又是各个面），
    // 没有 z-buffer 时后画的背面点会覆盖前面的点，深度图变成前/后景斑块拼贴，
    // 描边会在平面（比如立方体正面）上凭空画出一片圈状伪影。
    this.depthTarget = new THREE.WebGLRenderTarget(w, h, {
      minFilter: THREE.NearestFilter,
      magFilter: THREE.NearestFilter,
      type: this.depthTextureType,
      depthBuffer: true,
      stencilBuffer: false,
      generateMipmaps: false,
    });

    // 平滑 pass 只是全屏后处理，不需要深度
    this.smoothTarget = new THREE.WebGLRenderTarget(w, h, {
      minFilter: THREE.NearestFilter,
      magFilter: THREE.NearestFilter,
      type: this.depthTextureType,
      depthBuffer: false,
      stencilBuffer: false,
      generateMipmaps: false,
    });

    this.smoothUniforms.uTexel.value.set(1 / w, 1 / h);
    this.compositeUniforms.uTexel.value.set(1 / w, 1 / h);
    this.imagePrintUniforms.uTexel.value.set(1 / w, 1 / h);
    this.updateImageScale(w / Math.max(h, 1));

    // 点大小的换算：uPointSize 是「参考深度处的 CSS 像素」，乘 dpr 换到设备像素
    this.applyDepthRange();

    // 分辨率变了要重画一帧，否则会看到旧画面
    if (this.rafId !== 0) this.renderFrame();
  };

  // ── 循环 ────────────────────────────────────────────────────────────────
  start(): void {
    if (this.rafId !== 0) return;
    this.lastTickAt = 0;
    this.fpsWindowStart = performance.now();
    this.fpsFrames = 0;
    this.lastStatsAt = this.fpsWindowStart;
    this.rafId = requestAnimationFrame(this.tick);
  }

  stop(): void {
    if (this.rafId !== 0) cancelAnimationFrame(this.rafId);
    this.rafId = 0;
  }

  private readonly tick = (): void => {
    this.rafId = requestAnimationFrame(this.tick);
    this.frame += 1;

    const now = performance.now();
    const dt = this.lastTickAt === 0 ? 1 / 60 : Math.min((now - this.lastTickAt) / 1000, 0.1);
    this.lastTickAt = now;

    // L3 沸腾：相位由「按 60fps 折算的逻辑帧计 % 循环长度」决定。
    // 这样换 144Hz 屏幕也是 10 Hz 重绘（§9 推荐 8~12 fps 的「重画」感）。
    this.boilClock = (this.boilClock + dt * 60) % BOIL_CYCLE_FRAMES;
    this.updateBoil();

    if (this.params.autoSway) {
      // 自动运动（往返）。不是转圈：真实插画的深度只是剪影平板，转满一圈会露馅。
      this.swayClock = (this.swayClock + dt) % LIVE_SWAY_PERIOD;
      const phase = (this.swayClock / LIVE_SWAY_PERIOD) * Math.PI * 2;
      const sway = Math.sin(phase);
      if (this.params.parallaxMode) {
        // 视差平移：只挪机位，朝向恒定（平板永远平行于画面）
        const amplitude = this.parallaxAmplitude();
        this.poseParallax(amplitude * sway, 0.5 * amplitude * sway);
      } else {
        const amplitude = this.swayAmplitudeRad();
        const distance = this.camera.position.distanceTo(this.controls.target);
        this.poseFromOffsets(amplitude * sway, amplitude * SWAY_PITCH_RATIO * sway, distance);
      }
    } else {
      this.controls.update(dt);
      // 视差模式下用户拖动 = 平移；把基准位姿跟到当前位置
      if (this.params.parallaxMode) this.syncParallaxBaseFromCamera();
    }

    this.renderFrame();
    this.updateFps();
  };

  /** 沸腾相位：当前循环内第几相 */
  private updateBoil(): void {
    const p = this.params;
    if (!p.boil) {
      this.boilPhase = 0;
      this.pointUniforms.uBoilSeed.value = 0;
      this.compositeUniforms.uBoilJitter.value.set(0, 0);
      this.compositeUniforms.uBoilFlicker.value = 0;
      return;
    }

    const framesPerPhase = Math.max(1, Math.round(60 / BOIL_FPS));
    const phases = Math.max(1, Math.round(BOIL_CYCLE_FRAMES / framesPerPhase));
    this.applyBoilPhase(Math.floor(this.boilClock / framesPerPhase) % phases);
  }

  /** 把某个沸腾相位刷到 uniform 上（实时循环与录制共用同一套公式） */
  private applyBoilPhase(phase: number): void {
    this.boilPhase = phase;
    if (!this.params.boil) {
      this.pointUniforms.uBoilSeed.value = 0;
      this.compositeUniforms.uBoilJitter.value.set(0, 0);
      this.compositeUniforms.uBoilFlicker.value = 0;
      this.halftoneUniforms.uHalftoneOffset.value.set(0, 0);
      this.engravingUniforms.uScreenOffset.value.set(0, 0);
      this.imagePrintUniforms.uScreenOffset.value.set(0, 0);
      this.imagePrintUniforms.uBoilJitter.value.set(0, 0);
      this.imagePrintUniforms.uBoilFlicker.value = 0;
      return;
    }

    // 同一 phase 的抖动是确定的；不同 phase 之间用整数哈希取随机 → 不需存历史
    const r1 = hash01(phase * 3 + 1);
    const r2 = hash01(phase * 3 + 2);
    const r3 = hash01(phase * 3 + 3);

    // 点画：整套点的去留按 phase 重掷（vertex 端把 seed 掺进每点哈希）
    this.pointUniforms.uBoilSeed.value = phase;
    // 描边：采样位置微抖 + 阈值微抖，线条跟着"重描"一遍
    this.compositeUniforms.uBoilJitter.value.set(
      (r2 - 0.5) * 2 * BOIL_LINE_JITTER,
      (r3 - 0.5) * 2 * BOIL_LINE_JITTER,
    );
    this.compositeUniforms.uBoilFlicker.value = r1;

    // §3 抖动换序：网点网格按相位整体平移（< 一个 cell），网点「重排」而不是闪`r
    const cell = Math.max(this.halftoneUniforms.uHalftoneCell.value, 2);
    this.halftoneUniforms.uHalftoneOffset.value.set((r2 - 0.5) * cell * 0.7, (r3 - 0.5) * cell * 0.7);
    const hatchSpacing = Math.max(this.engravingUniforms.uHatchSpacing.value, 2);
    this.engravingUniforms.uScreenOffset.value.set(
      (r2 - 0.5) * hatchSpacing,
      (r3 - 0.5) * hatchSpacing,
    );
    // 图像模式：屏幕空间点画/网点/排线也按相位重排（< 一个格子，不会跳）
    this.imagePrintUniforms.uScreenOffset.value.set(
      (r2 - 0.5) * Math.max(hatchSpacing, 4),
      (r3 - 0.5) * Math.max(hatchSpacing, 4),
    );
    this.imagePrintUniforms.uBoilJitter.value.set(
      (r2 - 0.5) * 2 * BOIL_LINE_JITTER,
      (r3 - 0.5) * 2 * BOIL_LINE_JITTER,
    );
    this.imagePrintUniforms.uBoilFlicker.value = r1;
  }

  // ── 导出 PNG ────────────────────────────────────────────────────────────
  /**
   * 把当前画面导出成 PNG。
   *
   * 只输出图片（不做 GIF/视频）。`size` 非空时临时切到目标尺寸渲染一帧
   * （pixelRatio 强制 1，所以绘图缓冲就是目标像素数），导完立刻恢复视口尺寸。
   * toBlob 在同一个同步任务里紧跟 renderFrame 调用，paint buffer 还没被清掉。
   */
  async exportPng(size: { width: number; height: number } | null): Promise<Blob> {
    const restore = this.exportSize;
    this.exportSize = size;
    this.resize();
    this.renderFrame();
    const blob = await new Promise<Blob>((resolve, reject) => {
      this.canvas.toBlob((value) => {
        if (value) resolve(value);
        else reject(new Error('canvas.toBlob 返回空'));
      }, 'image/png');
    });
    this.exportSize = restore;
    this.resize();
    this.lastTickAt = 0;
    this.renderFrame();
    return blob;
  }
  private renderFrame(): void {
    const colorTarget = this.colorTarget;
    const depthTarget = this.depthTarget;
    const smoothTarget = this.smoothTarget;
    const colorPoints = this.colorPoints;
    const depthPoints = this.depthPoints;
    if (!colorTarget) return;

    const renderer = this.renderer;

    // ── 图像模式（不碰深度）：原图 → colorRT → 图像合成 pass ───────────────
    if (this.useImageMode()) {
      renderer.setRenderTarget(colorTarget);
      renderer.setClearColor(this.canvasBgColor, 1);
      renderer.clear();
      this.quadMesh.material = this.imageQuadMaterial;
      renderer.render(this.quadScene, this.quadCamera);

      renderer.setRenderTarget(null);
      renderer.setClearColor(this.canvasBgColor, 1);
      renderer.clear();
      this.imagePrintUniforms.tColor.value = colorTarget.texture;
      this.quadMesh.material = this.imagePrintMaterial;
      renderer.render(this.quadScene, this.quadCamera);
      return;
    }
    if (!depthTarget || !smoothTarget || !colorPoints || !depthPoints) return;

    // ── 1. 深度 pass ──────────────────────────────────────────────────────
    renderer.setRenderTarget(depthTarget);
    renderer.setClearColor(0xffffff, 1); // 背景 = 1.0 = 最远
    colorPoints.visible = false;
    depthPoints.visible = true;
    renderer.render(this.scene, this.camera);

    // ── 2. 双边平滑深度 ───────────────────────────────────────────────────
    renderer.setRenderTarget(smoothTarget);
    this.smoothUniforms.tDepth.value = depthTarget.texture;
    this.quadMesh.material = this.smoothMaterial;
    renderer.render(this.quadScene, this.quadCamera);

    // ── 3. 颜色 pass ──────────────────────────────────────────────────────
    renderer.setRenderTarget(colorTarget);
    renderer.setClearColor(this.canvasBgColor, 1); // 背景 = 纸色
    colorPoints.visible = true;
    depthPoints.visible = false;
    renderer.render(this.scene, this.camera);

    // ── 4. 合成到屏幕 ─────────────────────────────────────────────────────
    renderer.setRenderTarget(null);
    renderer.setClearColor(this.canvasBgColor, 1);
    this.compositeUniforms.tColor.value = colorTarget.texture;
    this.compositeUniforms.tDepth.value = smoothTarget.texture;
    this.halftoneUniforms.tColor.value = colorTarget.texture;
    this.halftoneUniforms.tDepth.value = smoothTarget.texture;
    this.engravingUniforms.tColor.value = colorTarget.texture;
    this.engravingUniforms.tDepth.value = smoothTarget.texture;
    // L2 风格层三选一：排线（铜版画）> 网点（半调）> 点云自带点画
    this.quadMesh.material =
      this.params.engraving > 0.001
        ? this.engravingMaterial
        : this.params.halftone > 0.001
          ? this.halftoneMaterial
          : this.compositeMaterial;
    renderer.render(this.quadScene, this.quadCamera);
  }

  private updateFps(): void {
    const now = performance.now();
    this.fpsFrames += 1;
    const elapsed = now - this.fpsWindowStart;
    if (elapsed >= 500) {
      this.fps = (this.fpsFrames * 1000) / elapsed;
      this.fpsFrames = 0;
      this.fpsWindowStart = now;
    }
    if (now - this.lastStatsAt >= 250) {
      this.lastStatsAt = now;
      this.onStats?.(this.stats());
    }
  }

  stats(): ViewerStats {
    const size = this.renderer.getDrawingBufferSize(new THREE.Vector2());
    return {
      fps: this.fps,
      frame: this.frame,
      points: this.colorPoints?.geometry.getAttribute('position')?.count ?? 0,
      drawCalls: this.renderer.info.render.calls,
      boilPhase: this.boilPhase,
      width: Math.floor(size.x),
      height: Math.floor(size.y),
      cameraDistance: this.frameInfo?.distance ?? 0,
      coreRadius: this.frameInfo?.radius ?? 0,
      pointSize: this.params.pointSize,
    };
  }

  /** 当前沸腾循环的可回绕时间（秒）：用于将来任何需要 uTime 的风格 */
  get wrappedTime(): number {
    return this.boilClock / 60;
  }

  /** 手动渲染一帧（截图 / 自动化测试用，不推进帧号） */
  renderOnce(): void {
    this.updateBoil();
    this.renderFrame();
  }

  dispose(): void {
    this.disposed = true;
    this.stop();
    window.removeEventListener('resize', this.resize);
    this.controls.dispose();
    this.colorPoints?.geometry.dispose();
    this.pointMaterial.dispose();
    this.depthMaterial.dispose();
    this.smoothMaterial.dispose();
    this.compositeMaterial.dispose();
    this.halftoneMaterial.dispose();
    this.engravingMaterial.dispose();
    this.quadGeometry.dispose();
    this.colorTarget?.dispose();
    this.depthTarget?.dispose();
    this.smoothTarget?.dispose();
    this.renderer.dispose();
  }
}

/** 自动化/调试入口：Playwright / scripts/shot.cjs 里读 window.illust3d.stats() */
export interface Illust3dDebug {
  viewer: Viewer;
  stats: () => ViewerStats;
  /** 外部脚本直接改 viewer.params 之后，用它把面板控件刷到当前值 */
  syncPanel?: () => void;
  /** 换一份 ILC1 点云（真实深度点云 / 自动化测试用），返回点数 */
  loadILC1?: (buffer: ArrayBuffer, label?: string) => number;
  /** 导出 PNG（size = null 表示当前视口） */
  exportPng?: (size: { width: number; height: number } | null) => Promise<Blob>;
  /** 相机 FOV（度）：越小越接近正交投影（42 = 默认透视，1 ≈ 正交） */
  setCameraFov?: (deg: number) => void;
  /** 当前相机 FOV（度） */
  cameraFov?: () => number;
}

declare global {
  interface Window {
    illust3d?: Illust3dDebug;
  }
}
