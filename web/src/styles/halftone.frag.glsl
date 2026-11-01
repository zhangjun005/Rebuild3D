// ─────────────────────────────────────────────────────────────────────────────
// 「半调漫画」halftone-manga 的合成 pass（L2 半调 + 深度描边 + L4 暗角/颗粒）
//
// 为什么半调放在**合成 pass**而不是点材质里：
//   半调是「对连续调图像做网点分色」的印刷过程，网屏必须是屏幕空间的连续网格。
//   如果在每个点精灵内部做网点，2~3 px 的点会被网点切碎，点与点之间没有连续性，
//   读起来是噪点而不是网点。所以：颜色 pass 先把点云画成一张连续调图像，
//   合成 pass 再对它做 §2.4 的网点化（这才是「半调」的本意）。
//
// 网点公式（§2.4）：
//   vec2 g = rot(fragCoord, screenAngle)
//   vec2 cell = fract(g / cellSize) - 0.5
//   float r = sqrt(1.0 - lum) * 0.72
//   if (length(cell) > r) discard
// 这里用 smoothstep 代替 discard（抗锯齿），并把网点映射成 墨/纸 两色。
//
// L3 抖动换序（§3）：网点网格按沸腾相位整体平移若干像素 → 手绘/印刷的「重画」感。
// 背景（纸）不做网点：用深度图判断哪里有点云覆盖。
// ─────────────────────────────────────────────────────────────────────────────

uniform sampler2D tColor;
uniform sampler2D tDepth;
uniform sampler2D tNoise;

uniform vec2 uTexel;
uniform vec3 uPaper;
uniform vec3 uInk;

// 半调
uniform float uHalftoneStrength; // 0..1 网点强度（0 时退回连续调）
uniform float uHalftoneCell; // 网点 cell 大小（像素）
uniform float uHalftoneAngle; // 网屏角度（弧度）
uniform float uDensityGamma; // 明暗曲线（>1 对比更强）
uniform vec2 uHalftoneOffset; // 抖动换序：网点网格平移（像素）

// 描边
uniform float uEdgeStrength;
uniform float uEdgeThreshold;
uniform float uEdgeWidth;
uniform vec2 uBoilJitter; // 描边采样抖动（像素）
uniform float uBoilFlicker;

// 后期
uniform float uVignette;
uniform float uGrain;

varying vec2 vUv;

const vec3 LUMA = vec3(0.299, 0.587, 0.114);

float depthAt(vec2 uv) {
  return texture2D(tDepth, uv).r;
}

void main() {
  vec3 color = texture2D(tColor, vUv).rgb;

  // ── L2 半调网点 ────────────────────────────────────────────────────────────
  // 亮度 → 网点半径：暗处点大（覆盖率高），亮处点小（留白）
  float lum = dot(color, LUMA);
  float tone = pow(clamp(lum, 0.0, 1.0), uDensityGamma);

  vec2 shifted = gl_FragCoord.xy + uHalftoneOffset;
  float cosA = cos(uHalftoneAngle);
  float sinA = sin(uHalftoneAngle);
  vec2 rotated = vec2(cosA * shifted.x - sinA * shifted.y, sinA * shifted.x + cosA * shifted.y);

  vec2 cell = fract(rotated / max(uHalftoneCell, 2.0)) - 0.5;
  // sqrt 让覆盖面积与明暗成线性（印刷网点半径 ∝ sqrt(覆盖率)）
  // 半径系数 0.5：tone=0 时网点刚好内切 cell（满黑），留出 0.5→0 的完整调子范围。
  // 用 0.72 会让中间调就糊成实黑（网点彼此粘连）。
  float radius = sqrt(1.0 - tone) * 0.5;
  float distanceToCenter = length(cell);
  float dots = 1.0 - smoothstep(radius - 0.07, radius + 0.07, distanceToCenter);

  // 只对「有点云的地方」做网点：背景保持纯纸色（墨/纸系风格里空背景就是纸）
  float coverage = 1.0 - smoothstep(0.97, 0.997, depthAt(vUv));
  vec3 halftoneColor = mix(uPaper, mix(uPaper, uInk, dots), coverage);
  vec3 outCol = mix(color, halftoneColor, clamp(uHalftoneStrength, 0.0, 1.0));

  // ── L2 粗描边：sobel on 平滑深度（与点画印刷同一套）──────────────────────
  vec2 uv = vUv + uBoilJitter * uTexel;
  vec2 o = uTexel * max(uEdgeWidth, 0.5);

  float tl = depthAt(uv + vec2(-o.x, o.y));
  float tc = depthAt(uv + vec2(0.0, o.y));
  float tr = depthAt(uv + vec2(o.x, o.y));
  float ml = depthAt(uv + vec2(-o.x, 0.0));
  float mr = depthAt(uv + vec2(o.x, 0.0));
  float bl = depthAt(uv + vec2(-o.x, -o.y));
  float bc = depthAt(uv + vec2(0.0, -o.y));
  float br = depthAt(uv + vec2(o.x, -o.y));

  float gx = (tr + 2.0 * mr + br) - (tl + 2.0 * ml + bl);
  float gy = (bl + 2.0 * bc + br) - (tl + 2.0 * tc + tr);
  float gradient = length(vec2(gx, gy));

  float thr = max(uEdgeThreshold, 1e-4) * (1.0 + 0.45 * uBoilFlicker);
  float edge = smoothstep(thr, thr * 1.8 + 1e-5, gradient);
  outCol = mix(outCol, uInk, clamp(edge * uEdgeStrength, 0.0, 1.0));

  // ── L4 暗角 + 纸颗粒 ──────────────────────────────────────────────────────
  vec2 p = vUv - 0.5;
  outCol *= 1.0 - uVignette * smoothstep(0.20, 0.80, length(p));
  float grain = texture2D(tNoise, gl_FragCoord.xy / 256.0).r - 0.5;
  outCol += grain * uGrain;

  gl_FragColor = vec4(outCol, 1.0);
}
