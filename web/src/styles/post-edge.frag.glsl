// ─────────────────────────────────────────────────────────────────────────────
// 合成 pass（L2 深度描边 + L4 暗角/颗粒）
//
// 输入：
//   tColor  —— 点云颜色（背景 = 纸色）
//   tDepth  —— **双边平滑后**的归一化线性深度（smooth-depth.frag.glsl 的输出）
//   tNoise  —— blue noise，用来做静态纸颗粒
//
// 描边思路：sobel(tDepth) 的梯度幅值 → 阈值 → 用墨色压进画面。
// 沸腾（L3）：采样 UV 整体微抖 + 阈值微抖，相位由 JS 按 frame 回绕算好（见 Viewer#updateBoil）。
// ─────────────────────────────────────────────────────────────────────────────

uniform sampler2D tColor;
uniform sampler2D tDepth;
uniform sampler2D tNoise;

uniform vec2 uTexel; // 1 / 绘图缓冲分辨率
uniform float uEdgeStrength; // 描边强度 0..2
uniform float uEdgeThreshold; // 描边阈值
uniform float uEdgeWidth; // 描边采样半径（像素）
uniform vec3 uInk; // 墨色
uniform float uVignette; // 暗角 0..1
uniform float uGrain; // 纸颗粒 0..1
uniform vec2 uBoilJitter; // 沸腾：描边采样抖动（像素）
uniform float uBoilFlicker; // 沸腾：阈值抖动 0..1

varying vec2 vUv;

float depthAt(vec2 uv) {
  return texture2D(tDepth, uv).r;
}

void main() {
  vec3 col = texture2D(tColor, vUv).rgb;

  // ── L2 深度描边：sobel on 平滑深度 ──────────────────────────────────────
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
  float g = length(vec2(gx, gy));

  // 细台阶（立方体两面之间的棱，只有 1~2 px 宽）在 sobel 上响应偏弱，
  // 用 1 px 尺度的 Laplacian 补一刀。**但必须由 sobel 响应把关**：
  // Laplacian 对任何 1 px 小台阶都敏感（包括深度图里点 splat 的边缘），
  // 不设门就会把点本身的轮廓描成一圈黑边。
  float lap = abs(
    4.0 * depthAt(uv) - (
      depthAt(uv + vec2(0.0, -uTexel.y)) +
      depthAt(uv + vec2(0.0, uTexel.y)) +
      depthAt(uv + vec2(-uTexel.x, 0.0)) +
      depthAt(uv + vec2(uTexel.x, 0.0))
    )
  );

  float thr = max(uEdgeThreshold, 1e-4) * (1.0 + 0.45 * uBoilFlicker);
  // 门控拉紧：Laplacian 只在 sobel 已经明显有响应时才允许增强，
  // 否则深度图里点 splat 的边缘会被描成一圈圈小环（近侧视的薄面尤其明显）。
  float enable = smoothstep(thr * 0.9, thr * 1.5, g);
  float response = max(g, lap * 0.5 * enable);

  float edge = smoothstep(thr, thr * 1.8 + 1e-5, response);

  vec3 outCol = mix(col, uInk, clamp(edge * uEdgeStrength, 0.0, 1.0));

  // ── L4 暗角 ────────────────────────────────────────────────────────────
  vec2 p = vUv - 0.5;
  outCol *= 1.0 - uVignette * smoothstep(0.20, 0.80, length(p));

  // ── L4 纸颗粒（静态 blue noise，不随帧变）─────────────────────────────
  float grain = texture2D(tNoise, gl_FragCoord.xy / 256.0).r - 0.5;
  outCol += grain * uGrain;

  gl_FragColor = vec4(outCol, 1.0);
}
