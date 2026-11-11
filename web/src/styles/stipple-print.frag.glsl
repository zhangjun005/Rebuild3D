// @kron:intent render/style-layering/primitive-layer
// ─────────────────────────────────────────────────────────────────────────────
// 「点画印刷」stipple-print 的 fragment 端
//
// 组合（docs/rendering-styles.md §7 第一套预设）：
//   L1 基元 B 软边圆片   —— §1 的核心 GLSL（本文件主体）
//   L2 点画             —— 已在 points.vert.glsl 里逐点抖动完成（点要么画要么不画）
//   L3 沸腾             —— 相位由 JS 按「可回绕帧号」算好，vertex 端吃 uBoilOffset
//   L4 空气透视 + 单色纸墨 —— 本文件与 vertex 端用同一套公式，保证密度和颜色一致
//
// 颜色空间：全链路跑在「显示色彩空间」（ColorManagement 关闭，见 viewer.ts），
// 因为 ILC1 里的 u8 颜色是作者意图色，不是物理辐亮度；风格化渲染也不该先线化再加花。
// ─────────────────────────────────────────────────────────────────────────────

uniform vec3 uPaper; // 纸色（= 背景色，空气透视的终点色）
uniform vec3 uInk; // 墨色（单色纸墨的暗端）

uniform float uSoftness; // L1 软边 0..1
uniform float uMono; // L4 单色纸墨 0..1
uniform float uFogAmount; // L4 空气透视强度 0..1

varying vec3 vColor;
varying float vFog;

void main() {
  // ── L1 基元 B：软边圆片（§1 的核心 5 行）──────────────────────────────
  vec2 c = gl_PointCoord - 0.5;
  float r = length(c);
  if (r > 0.5) discard;
  float inner = mix(0.5, 0.30, clamp(uSoftness, 0.0, 1.0));
  float alpha = smoothstep(0.5, inner, r);

  // ── L4 空气透视：向纸色收 ──────────────────────────────────────────────
  vec3 col = mix(vColor, uPaper, vFog * uFogAmount);

  // ── L2/L4 单色墨点：所有墨点用同一个墨色，明暗**只**由点的疏密表达 ──
  // 这是点画的关键：如果点色也跟着亮度变化（duotone），密度和深浅会双重计数，
  // 中间调会糊成一片灰。印刷上就是一个墨、一张纸，暗部靠覆盖率。
  col = mix(col, uInk, clamp(uMono, 0.0, 1.0));

  gl_FragColor = vec4(col, alpha);
}
