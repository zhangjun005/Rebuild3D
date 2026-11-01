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
uniform float uToneLow; // 自动色调范围下界（点云亮度的 5 分位）
uniform float uToneHigh; // 自动色调范围上界（95 分位）

attribute vec3 aColor; // u8 归一化颜色（0..1）
attribute float aRandom; // 每点随机 0..1（CPU 端生成，与位置无关！）

varying vec3 vColor;
varying float vFog; // 0 = 近，1 = 远

const vec3 LUMA = vec3(0.299, 0.587, 0.114);

// ★ 为什么点画抽稀用 aRandom 而不是位置哈希：
//   真实深度点云是**规则网格**（深度图 504×504 逐像素反投影），对格点做位置哈希
//   会让相邻格点的哈希值相关 → 屏幕上出现条带状摩尔纹（真实插画实测可见）。
//   CPU 端生成的 aRandom 真正与位置无关。

void main() {
  // ── 点数密度：逐点均匀抽稀（用 aRandom，与位置无关）─────────────────────
  if (uDensity < 0.999 && aRandom > uDensity) {
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
    // 自动色调范围（auto-levels）：真实插画的亮度可能整片偏亮（比如白底角色），
    // 直接用绝对亮度会让「亮部全成空白纸」。按 5~95 分位归一化后才谈得上疏密对比。
    float lum = clamp(
      (dot(pc, LUMA) - uToneLow) / max(uToneHigh - uToneLow, 1e-3),
      0.0,
      1.0
    );

    // §2.1「暗处点密、亮处点疏」：墨水点在纸上是靠覆盖率表达暗部，
    // 所以保留概率 = 1 - 亮度；高光处趋 0（留白），暗部覆盖率最高。
    float density =
      pow(clamp(1.0 - lum, 0.0, 1.0), uDensityGamma) * (1.0 - vFog * uFogAmount);

    // 沸腾：种子一变，整套点的去留重掷一次 → 手绘动画的「重画」感（§3）。
    // aRandom 是每点独立随机，叠加相位种子后仍然是"重掷"而不是"平移"。
    float r = fract(aRandom + uBoilSeed * 0.6180339887);

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
