// ─────────────────────────────────────────────────────────────────────────────
// 深度 pass（离屏）—— 给屏幕空间描边提供归一化线性深度。
//
// 为什么不用主 pass 的 depth buffer：
//   1. 深度需要用 HalfFloat 存归一化后的线性深度，精度才够（§9「深度图精度不足会闪」）；
//   2. 点云的深度有洞（点之间有空隙），这一 pass 故意把点放大一点封住洞，
//      平滑在下一步用双边滤波做（smooth-depth.frag.glsl）。
// 点大小放大系数 uGrow 只影响深度图，不影响画面。
// ─────────────────────────────────────────────────────────────────────────────

uniform float uPointSize;
uniform float uScale;
uniform float uGrow;
uniform float uDepthNear;
uniform float uDepthFar;

varying float vDepth;

void main() {
  vec4 mvPosition = modelViewMatrix * vec4(position, 1.0);
  gl_Position = projectionMatrix * mvPosition;

  float depth = max(-mvPosition.z, 1e-4);
  gl_PointSize = clamp(uPointSize * uScale / depth * uGrow, 1.0, 255.0);

  // 归一化到 [uDepthNear, uDepthFar]：这是「风格关心的深度区间」，不是相机 near/far。
  vDepth = clamp((depth - uDepthNear) / (uDepthFar - uDepthNear), 0.0, 1.0);
}
