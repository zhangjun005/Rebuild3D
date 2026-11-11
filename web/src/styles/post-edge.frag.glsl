// @kron:intent render/style-layering/composite/post-edge
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

// ── 彩色点画（保留原色）的印刷叠加层 ─────────────────────────────────────
// 关键：质感**不参与点生成**。点只负责带颜色，网点/纸纹/色调分离都在这里叠。
uniform float uColorMode; // 1 = 彩色点画路径
uniform float uColorPosterize; // 色调分离级数（0/1 = 不分离）
uniform float uColorHalftone; // 屏幕空间网点叠加强度
uniform float uColorSaturation; // 饱和度增益（围绕亮度放大色度，不动明暗）
uniform float uColorCell; // 网点 cell 像素
uniform float uColorAngle; // 网点角度（弧度）
uniform sampler2D tPaperNoise; // 复用 blue noise 当纸纹

varying vec2 vUv;

float depthAt(vec2 uv) {
  return texture2D(tDepth, uv).r;
}

const vec3 LUMA = vec3(0.299, 0.587, 0.114);

/** 彩色点画：色调分离 → 屏幕空间网点叠墨 → 纸纹 */
vec3 printify(vec3 src) {
  vec3 col = src;
  // 1) 色调分离：**只量化亮度，保留色度**
  //
  // 为什么不能逐 RGB 通道量化：三个通道各自被拉到最近档位，色相/饱和度会被破坏
  // （实测 posterize=6 时脸/眼睛直接糊成一坨，背景却更浓）。
  // 这里按亮度算出一个标量 Lq，再把颜色按 Lq/Lum 等比缩放：
  //   - 明暗被压成 N 档（印刷的分层感）
  //   - 颜色比例不变（粉色还是粉的，脸不被通道量化打碎）
  float levels = uColorPosterize;
  if (levels >= 2.0) {
    float lum = clamp(dot(col, LUMA), 0.0, 1.0);
    float lq = floor(lum * levels + 0.5) / levels;
    col = col * (lq / max(lum, 0.02));
    col = clamp(col, 0.0, 1.0);
  }
  // 1.5) 饱和度增益：把色度围绕亮度放大（不动明暗）
  //      点云渲染整体比原图淡（点缝露纸 + 色调分离），这是对症的干净补偿。
  float lumSat = clamp(dot(col, LUMA), 0.0, 1.0);
  col = clamp(mix(vec3(lumSat), col, uColorSaturation), 0.0, 1.0);

  // 2) 屏幕空间网点：暗部叠墨点多一些，亮部留白 → 印刷的调子层
  float lum = clamp(dot(col, LUMA), 0.0, 1.0);
  vec2 shifted = gl_FragCoord.xy + uBoilJitter * 4.0;
  float c = cos(uColorAngle);
  float s = sin(uColorAngle);
  vec2 rotated = vec2(c * shifted.x - s * shifted.y, s * shifted.x + c * shifted.y);
  vec2 cell = fract(rotated / max(uColorCell, 2.0)) - 0.5;
  float radius = sqrt(clamp(1.0 - lum, 0.0, 1.0)) * 0.5;
  float dots = 1.0 - smoothstep(radius - 0.07, radius + 0.07, length(cell));
  col = mix(col, uInk, dots * uColorHalftone * 0.55 * (1.0 - lum));
  // 3) 纸纹：低频纸色偏移 + 高频颗粒（不改变构图）
  float speck = texture2D(tPaperNoise, gl_FragCoord.xy / 128.0).r - 0.5;
  col += speck * (0.035 + uGrain);
  float fibre = texture2D(tPaperNoise, gl_FragCoord.xy / 512.0).r - 0.5;
  col += fibre * 0.02;
  return clamp(col, 0.0, 1.0);
}

void main() {
  vec3 col = texture2D(tColor, vUv).rgb;
  if (uColorMode > 0.5) {
    col = printify(col);
  }

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
