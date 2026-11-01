// ─────────────────────────────────────────────────────────────────────────────
// L1 基元 B「软边圆片」的 vertex 端
//   + L2 点画（逐点抖动，见下）
//   + L3 沸腾（相位种子）
//   + L4 空气透视 / 深度衰减点大小
//
// 点大小（§1）：
//   gl_PointSize = uPointSize * uScale / -mvPosition.z
//   uPointSize 定义为「参考深度 uRefDist 处的屏幕像素直径」（美术参数，不是物理尺寸），
//   uScale = uRefDist * devicePixelRatio，于是任何 DPR 下观感一致。
//
// ★ 点画为什么是「逐点哈希」而不是 §8 的屏幕空间 blue noise（实测结论，见报告）：
//   屏幕空间噪声采样在**规则点阵**上会拍频出摩尔纹。测试点云的球面是 Fibonacci 点阵
//   （间距约 5 px），blue noise 的 cell 是 2.5 px，两者拍出整片斜向条纹 —— 球面被纹上
//   一圈圈假纹理，疏密关系全乱。这不是参数能调掉的，是采样定理问题。
//   §2.1 自己列了三条路线，第二条「逐点抖动」正是为这种情况准备的：
//   每个点用自己的确定性哈希做保留决策 → 与屏幕无关、转动不游动、没有摩尔纹、3D 稳定。
//   而「保留概率 = 1 - 亮度」保证暗处点密、亮处点疏（§2.1 / §5 的核心）。
//   blue noise 纹理没有浪费：L4 纸颗粒（post-edge.frag.glsl）用它，
//   后续半调 / 抖动换序（§3）也是同一张图。
// ─────────────────────────────────────────────────────────────────────────────

uniform float uPointSize; // 参考深度处的像素直径（UI「点大小」）
uniform float uScale; // = uRefDist * pixelRatio
uniform float uMinSize;
uniform float uMaxSize; // gl_PointSize 驱动上限的兜底（§9 第一坑）
uniform float uFogNear;
uniform float uFogFar;
uniform float uDensity; // 点数比例（逐点均匀抽稀）

uniform float uStipple; // 点画强度 0..1
uniform float uDensityGamma; // 明暗 → 密度曲线
uniform float uFogAmount; // 空气透视强度
uniform vec3 uPaper; // 纸色（空气透视终点）
uniform float uBoilSeed; // 沸腾：每相一个整数种子（相位由 JS 按可回绕时钟算）

attribute vec3 aColor; // u8 归一化颜色（0..1）

varying vec3 vColor;
varying float vFog; // 0 = 近，1 = 远

const vec3 LUMA = vec3(0.299, 0.587, 0.114);

/** 确定性 3D → [0,1) 哈希（Dave Hoskins hash13），GPU 上精度稳定 */
float hash13(vec3 p3) {
  p3 = fract(p3 * 0.1031);
  p3 += dot(p3, p3.yzx + 33.33);
  return fract((p3.x + p3.y) * p3.z);
}

void main() {
  // ── 点数密度：逐点均匀抽稀 ──────────────────────────────────────────────
  // 不用 geometry.setDrawRange 的原因：ILC1 的点是按「地面 → 球 → 立方体」排的，
  // 按抽头截断会让密度一降就只剩地面。逐点哈希在空间上均匀，转相机也不跳（§5）。
  if (uDensity < 0.999 && hash13(position + 1.7) > uDensity) {
    gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
    gl_PointSize = 0.0;
    return;
  }

  vec4 mvPosition = modelViewMatrix * vec4(position, 1.0);
  gl_Position = projectionMatrix * mvPosition;

  float depth = max(-mvPosition.z, 1e-4);
  float size = uPointSize * uScale / depth;

  vColor = aColor;
  vFog = smoothstep(uFogNear, uFogFar, depth);

  // ── L2 点画 + L3 沸腾：逐点抖动 ─────────────────────────────────────────
  if (uStipple > 0.001) {
    // 保留概率只看亮度（空气透视会把远处推亮 → 远处自然变稀）。
    // 单色墨点模式下点色是统一的墨色，所以这里不掺 duotone：明暗只由疏密表达。
    vec3 pc = mix(aColor, uPaper, vFog * uFogAmount);
    float lum = dot(pc, LUMA);

    // §2.1「暗处点密、亮处点疏」：墨水点在纸上是靠覆盖率表达暗部，
    // 所以保留概率 = 1 - 亮度；高光处趋 0（留白），暗部覆盖率最高。
    float density =
      pow(clamp(1.0 - lum, 0.0, 1.0), uDensityGamma) * (1.0 - vFog * uFogAmount);

    // 沸腾：种子一变，整套点的去留重掷一次 → 手绘动画的「重画」感（§3）。
    // 种子来自可回绕时钟，所以 48 帧一循环不跳帧（§9「GIF 循环跳帧」）。
    float seed = uBoilSeed;
    vec3 seedOffset = vec3(seed * 13.17, seed * 7.31, seed * 3.77);
    float r = hash13(position * 1.37 + seedOffset);

    if (r > mix(1.0, density, clamp(uStipple, 0.0, 1.0))) {
      gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
      gl_PointSize = 0.0;
      return;
    }

    // §5 的第三种做法：密处点略大一点，增加墨色覆盖（视觉上更接近"密度"）
    size *= mix(1.0, 0.82 + 0.45 * density, clamp(uStipple, 0.0, 1.0));
  }

  gl_PointSize = clamp(size, uMinSize, uMaxSize);
}
