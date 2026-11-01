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
 * 取景留边。0.9 = 核心半径略微溢出画面：主体占满画面（75 分位半径已经剔除了
 * 地面/背景点的长尾，所以轻微溢出不会切到主体）。
 */
const FRAME_MARGIN = 0.9;
/** 点大小自适应：点径 ≈ 屏幕点间距 × 这个系数（0.5 → 相邻点略微交叠） */
const POINT_SIZE_PER_SPACING = 0.5;
/** 自适应点大小的上下限（CSS 像素） */
const POINT_SIZE_MIN = 1.2;
const POINT_SIZE_MAX = 8.0;

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
  /** 是否正在录制/出帧 */
  capturing: boolean;
}

// ─────────────────────────────────────────────────────────────────────────────
// 录制 / 出帧：固定 30fps 的确定性时钟
//
// 关键：**不走真实时间**。捕获时一切随时间变化的状态都由「帧索引 i」直接算出来：
//   旋转方位角 azimuth(i) = azimuth0 + 2π · turns · (i mod N) / N
//   沸腾相位   phase(i)   = floor((i mod N) / framesPerPhase) mod phases
// 于是状态是 i 的纯函数，且 i = N 与 i = 0 逐位相同（因为先取模），
// 这就是无缝循环的形式化保证（对齐算式见 README / 报告）。
// ─────────────────────────────────────────────────────────────────────────────

/** 捕获的固定输出帧率：不跟显示器刷新率走（144Hz 屏上录出来也是稳定的 30fps） */
export const CAPTURE_FPS = 30;

export interface CaptureSpec {
  /** 总帧数 = 秒数 × CAPTURE_FPS */
  totalFrames: number;
  /** 循环里转几圈（整数，默认 1）；结束方位角 = 起始 + 2π·turns */
  turns?: number;
  /** 沸腾重绘频率（Hz，默认 10 = §9 推荐的 8~12） */
  boilFps?: number;
}

export interface CapturePlan {
  totalFrames: number;
  turns: number;
  framesPerPhase: number;
  phases: number;
  /** 实际沸腾重绘频率 */
  boilHz: number;
  /** 循环时长（秒） */
  seconds: number;
}

interface CaptureState extends CapturePlan {
  azimuth0: number;
  elevation: number;
  distance: number;
  target: THREE.Vector3;
  savedPosition: THREE.Vector3;
  savedTarget: THREE.Vector3;
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

  private readonly pointUniforms: PointUniforms;
  private readonly depthUniforms: DepthUniforms;
  private readonly smoothUniforms: SmoothUniforms;
  private readonly compositeUniforms: CompositeUniforms;

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

  /** 画布（录制 / 出帧要用） */
  readonly canvas: HTMLCanvasElement;
  /** 录制/出帧的确定性时钟状态（非空 = 正在捕获） */
  private capture: CaptureState | null = null;
  /** 输出尺寸覆盖（null = 跟窗口走） */
  private outputSize: { width: number; height: number } | null = null;

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
    this.controls.minDistance = 1.4;
    this.controls.maxDistance = 14;
    this.controls.maxPolarAngle = Math.PI * 0.492; // 不钻到地面以下
    this.controls.autoRotateSpeed = 0.9;
    this.controls.update();

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

    this.quadMesh = new THREE.Mesh(this.quadGeometry, this.smoothMaterial);
    this.quadMesh.frustumCulled = false;
    this.quadScene.add(this.quadMesh);

    this.resize();
    this.applyParams();
    window.addEventListener('resize', this.resize);
  }

  // ── 数据 ────────────────────────────────────────────────────────────────
  /** 挂上点云（BufferGeometry 零拷贝引用 ILC1 的视图），并按包围盒自适应取景 */
  setCloud(cloud: IlcCloud, source?: string): void {
    if (cloud.count <= 0) throw new Error('点云是空的（count = 0）');

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
    geometry.computeBoundingBox();
    geometry.computeBoundingSphere();

    this.colorPoints = new THREE.Points(geometry, this.pointMaterial);
    this.depthPoints = new THREE.Points(geometry, this.depthMaterial);
    this.depthPoints.visible = false;

    this.scene.add(this.colorPoints, this.depthPoints);

    this.frameCloud(geometry, position);

    if (source !== undefined) this.onSource?.(source);
  }

  /**
   * 按点云的包围盒做自适应取景 + 自适应尺度参数。
   *
   * 真实点云（单图深度估计）的尺度、位置、朝向、点数都和测试点云不一样，
   * 所以这些量一个都不能写死：
   *   - 相机距离 / 目标点 → 由「核心半径」决定（75 分位，不让大片地面/背景把相机拉飞）
   *   - uPointSize 的参考视距 refDepth → 跟着取景距离走，换尺度后点的屏幕大小不变
   *   - 空气透视 / 描边归一化的深度区间 → 同上
   *   - 点大小 → 由估算的「屏幕点间距」决定，点数变少时点自动变大，覆盖率不塌
   */
  private frameCloud(geometry: THREE.BufferGeometry, position: THREE.BufferAttribute): void {
    const count = position.count;
    const box = geometry.boundingBox ?? new THREE.Box3().setFromBufferAttribute(position);
    const center = box.getCenter(new THREE.Vector3());
    const size = box.getSize(new THREE.Vector3());
    const diagonal = Math.max(size.length(), 1e-6);

    // 核心半径：到中心距离的 75 分位（稳健，不被少数离群点/地面拉飞）
    const radius = Math.max(this.percentileRadius(position, center, FRAME_RADIUS_PERCENTILE), 1e-6);

    const fovRad = THREE.MathUtils.degToRad(this.camera.fov);
    const fitDistance = (radius / Math.sin(fovRad / 2)) * FRAME_MARGIN;

    // 相机方向保持 3/4 俯视（艺术取景，不是自动寻找最佳视角）
    const dir = new THREE.Vector3(0.34, 0.36, 0.87).normalize();
    this.controls.target.copy(center);
    this.camera.position.copy(center).addScaledVector(dir, fitDistance);
    this.camera.near = Math.max(fitDistance * 0.005, 1e-5);
    this.camera.far = fitDistance * 12 + diagonal * 2;
    this.camera.updateProjectionMatrix();

    this.controls.minDistance = radius * 0.25;
    this.controls.maxDistance = fitDistance * 6;
    this.controls.maxPolarAngle = Math.PI * 0.492;
    this.controls.update();

    this.frameInfo = { center: center.clone(), radius, distance: fitDistance };

    // 尺度相关参数：换尺度后观感保持一致
    this.refDepth = fitDistance * 0.91;
    this.depthNear = fitDistance * 0.57;
    this.depthFar = fitDistance * 1.82;
    this.applyDepthRange();

    // 点大小自适应：点云来自深度图，基本是 2.5D 面 → 间距 ≈ 对角线 / sqrt(点数)
    const spacingWorld = diagonal / Math.sqrt(count);
    const cssHeight = Math.max(window.innerHeight, 1);
    const pxPerWorldAtRef = (cssHeight * 0.5) / Math.tan(fovRad / 2) / this.refDepth;
    const spacingPx = spacingWorld * pxPerWorldAtRef;
    const autoPointSize = THREE.MathUtils.clamp(
      spacingPx * POINT_SIZE_PER_SPACING,
      POINT_SIZE_MIN,
      POINT_SIZE_MAX,
    );
    this.params.pointSize = autoPointSize;
    this.applyParams();
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

  /** 应用某个预设 */
  applyPreset(key: PresetKey): void {
    this.setParams(cloneParams(PRESETS[key].params));
  }

  private applyParams(): void {
    const p = this.params;

    this.paperColor.set(p.paper);
    this.pointUniforms.uPaper.value = this.paperColor;

    this.pointUniforms.uPointSize.value = p.pointSize;
    this.pointUniforms.uSoftness.value = p.softness;
    this.pointUniforms.uDensity.value = p.density;
    this.pointUniforms.uStipple.value = p.stipple;
    this.pointUniforms.uDensityGamma.value = p.densityGamma;
    this.pointUniforms.uMono.value = p.mono;
    this.pointUniforms.uFogAmount.value = p.fogAmount;

    this.compositeUniforms.uEdgeStrength.value = p.edgeStrength;
    this.compositeUniforms.uEdgeThreshold.value = p.edgeThreshold;
    this.compositeUniforms.uEdgeWidth.value = p.edgeWidth;
    this.compositeUniforms.uVignette.value = p.vignette;
    this.compositeUniforms.uGrain.value = p.grain;

    this.controls.autoRotate = p.autoRotate;
  }

  // ── 尺寸 ────────────────────────────────────────────────────────────────
  private readonly resize = (): void => {
    if (this.disposed) return;
    // 录制时用输出尺寸覆盖窗口尺寸，pixelRatio 固定 1 → 绘图缓冲就是目标像素数
    const override = this.outputSize;
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

    // 捕获时：主循环只负责统计（保持 144Hz 的 rAF 心跳），
    // 画面由 capture.ts 的录制循环按 30fps 时间片渲染确定性帧。
    if (this.capture) {
      this.updateFps();
      return;
    }

    // L3 沸腾：相位由「按 60fps 折算的逻辑帧计 % 循环长度」决定。
    // 这样做同时满足两件事：① 换 144Hz 屏幕也是 10 Hz 重绘；② 时间可回绕（§9「GIF 循环跳帧」）。
    this.boilClock = (this.boilClock + dt * 60) % BOIL_CYCLE_FRAMES;
    this.updateBoil();

    this.controls.update(dt);
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
  }

  // ── 录制 / 出帧 ────────────────────────────────────────────────────────
  get isCapturing(): boolean {
    return this.capture !== null;
  }

  /** 当前输出帧尺寸（录制时是 setOutputSize 的目标，否则是窗口尺寸） */
  captureSize(): { width: number; height: number } {
    const size = this.outputSize;
    return size
      ? { width: size.width, height: size.height }
      : { width: window.innerWidth, height: window.innerHeight };
  }

  /**
   * 开始捕获：冻结交互、切到「帧索引驱动」的确定性时钟。
   * 返回对齐后的计划（实际沸腾频率可能因整除被微调）。
   */
  beginCapture(spec: CaptureSpec): CapturePlan {
    if (this.capture) throw new Error('已经处在捕获状态');

    const total = Math.max(1, Math.round(spec.totalFrames));
    const turns = Math.max(1, Math.round(spec.turns ?? 1));
    const boilFps = Math.max(1, spec.boilFps ?? 10);

    // 沸腾每相几帧：从 30/boilFps 往小取，直到能整除总帧数（保证相位在 N 处回绕到 0）
    let framesPerPhase = Math.max(1, Math.round(CAPTURE_FPS / boilFps));
    while (framesPerPhase > 1 && total % framesPerPhase !== 0) framesPerPhase -= 1;
    if (total % framesPerPhase !== 0) framesPerPhase = 1; // 兜底：一帧一相
    const phases = total / framesPerPhase;

    const target = this.controls.target.clone();
    const offset = this.camera.position.clone().sub(target);
    const distance = Math.max(offset.length(), 1e-6);

    this.capture = {
      totalFrames: total,
      turns,
      framesPerPhase,
      phases,
      boilHz: CAPTURE_FPS / framesPerPhase,
      seconds: total / CAPTURE_FPS,
      azimuth0: Math.atan2(offset.x, offset.z),
      elevation: Math.asin(THREE.MathUtils.clamp(offset.y / distance, -1, 1)),
      distance,
      target,
      savedPosition: this.camera.position.clone(),
      savedTarget: target.clone(),
    };

    this.controls.enabled = false;
    this.controls.autoRotate = false;
    return {
      totalFrames: total,
      turns,
      framesPerPhase,
      phases,
      boilHz: this.capture.boilHz,
      seconds: this.capture.seconds,
    };
  }

  /**
   * 渲染捕获序列里的第 index 帧。
   * index == totalFrames 时得到的就是「回绕帧」——与 index 0 逐位相同（自检用）。
   */
  renderCaptureFrame(index: number): void {
    const capture = this.capture;
    if (!capture) throw new Error('未开始捕获');

    const wrapped = ((index % capture.totalFrames) + capture.totalFrames) % capture.totalFrames;
    const angle = Math.PI * 2 * capture.turns * (wrapped / capture.totalFrames);
    const azimuth = capture.azimuth0 + angle;
    const cosElevation = Math.cos(capture.elevation);

    this.camera.position.set(
      capture.target.x + capture.distance * cosElevation * Math.sin(azimuth),
      capture.target.y + capture.distance * Math.sin(capture.elevation),
      capture.target.z + capture.distance * cosElevation * Math.cos(azimuth),
    );
    this.camera.lookAt(capture.target);
    this.camera.updateMatrixWorld();

    const phase = Math.floor(wrapped / capture.framesPerPhase) % capture.phases;
    this.applyBoilPhase(phase);

    this.renderFrame();
  }

  /** 结束捕获：恢复相机与交互 */
  endCapture(): void {
    const capture = this.capture;
    if (!capture) return;
    this.capture = null;
    this.camera.position.copy(capture.savedPosition);
    this.controls.target.copy(capture.savedTarget);
    this.controls.enabled = true;
    this.lastTickAt = 0;
    this.applyParams();
    this.controls.update();
    this.renderFrame();
  }

  /**
   * 设置输出尺寸（null = 跟窗口走）。
   * 录制 1080p / 1080×1080 时用：pixelRatio 强制 1，绘图缓冲就是目标像素数。
   */
  setOutputSize(size: { width: number; height: number } | null): void {
    this.outputSize = size;
    if (size) {
      document.body.classList.add('capturing');
      const scale = Math.min(window.innerWidth / size.width, window.innerHeight / size.height);
      this.canvas.style.width = `${Math.max(1, Math.round(size.width * scale))}px`;
      this.canvas.style.height = `${Math.max(1, Math.round(size.height * scale))}px`;
    } else {
      document.body.classList.remove('capturing');
      this.canvas.style.width = '';
      this.canvas.style.height = '';
    }
    this.resize();
  }

  /** 捕获计划信息（未在捕获时返回 null） */
  get capturePlan(): CapturePlan | null {
    if (!this.capture) return null;
    const { totalFrames, turns, framesPerPhase, phases, boilHz, seconds } = this.capture;
    return { totalFrames, turns, framesPerPhase, phases, boilHz, seconds };
  }

  private renderFrame(): void {
    const colorTarget = this.colorTarget;
    const depthTarget = this.depthTarget;
    const smoothTarget = this.smoothTarget;
    const colorPoints = this.colorPoints;
    const depthPoints = this.depthPoints;
    if (!colorTarget || !depthTarget || !smoothTarget || !colorPoints || !depthPoints) return;

    const renderer = this.renderer;

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
    renderer.setClearColor(this.paperColor, 1); // 背景 = 纸色
    colorPoints.visible = true;
    depthPoints.visible = false;
    renderer.render(this.scene, this.camera);

    // ── 4. 合成到屏幕 ─────────────────────────────────────────────────────
    renderer.setRenderTarget(null);
    renderer.setClearColor(this.paperColor, 1);
    this.compositeUniforms.tColor.value = colorTarget.texture;
    this.compositeUniforms.tDepth.value = smoothTarget.texture;
    this.quadMesh.material = this.compositeMaterial;
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
      capturing: this.capture !== null,
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
    this.quadGeometry.dispose();
    this.colorTarget?.dispose();
    this.depthTarget?.dispose();
    this.smoothTarget?.dispose();
    this.renderer.dispose();
  }
}

/** 自动化/调试入口：Playwright 里读 window.illust3d.stats() */
export interface Illust3dDebug {
  viewer: Viewer;
  stats: () => ViewerStats;
  /** 外部脚本直接改 viewer.params 之后，用它把面板控件刷到当前值 */
  syncPanel?: () => void;
  /** 换一份 ILC1 点云（真实深度点云 / 自动化测试用），返回点数 */
  loadILC1?: (buffer: ArrayBuffer, label?: string) => number;
  /** A 阶段约定：逐帧导出 PNG blob（GIF 由 Go 侧 image/gif 编码） */
  captureFrames?: (
    count: number,
    options?: { turns?: number; size?: { width: number; height: number }; includeWrapFrame?: boolean },
  ) => Promise<Blob[]>;
  /** 录一个完整循环的 WebM */
  recordWebM?: (
    seconds: number,
    sizeKey?: 'viewport' | '1080p' | 'square',
  ) => Promise<{ blob: Blob; plan: CapturePlan; mimeType: string }>;
  /** 无缝自检：index 0 与 index N 是否逐像素相同 */
  verifySeam?: (
    seconds: number,
    sizeKey?: 'viewport' | '1080p' | 'square',
  ) => Promise<{ identical: boolean; hashFirst: string; hashWrap: string; plan: CapturePlan }>;
}

declare global {
  interface Window {
    illust3d?: Illust3dDebug;
  }
}
