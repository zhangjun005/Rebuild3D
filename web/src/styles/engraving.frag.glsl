// ─────────────────────────────────────────────────────────────────────────────
// 「铜版画」engraving 的合成 pass（§2.2 排线 + §7 第 2 套）
//
// 和半调同一个理由放在**合成 pass**：排线是"对连续调图像做网屏/加网"的印刷过程，
// 线必须是屏幕空间的连续图案；在 2~3 px 的点精灵内部画线等于把线打碎。
// 颜色 pass 先把点云画成连续调图像（stipple=0、小点径），这里再加排线。
//
// 做法：
//   1. 亮度 → 调子 curve → posterize 成 N 档（§2.2/§7 的 posterize(3~4)）
//   2. 3 层交叉排线（层间角差 45°），越暗的档位叠加越多层（层数 = 墨色浓度）
//   3. duotone：纸黄 ↔ 墨褐（uPaper / uInk）
//   4. 深度描边叠在最上面（铜版画的轮廓线）
//
// 参数的物理含义：排线间距是屏幕像素，层数决定最深能压到多黑；
// 层间角差固定 45°（金属版画传统，30° 会互相拍频出摩尔纹）。
// ─────────────────────────────────────────────────────────────────────────────

uniform sampler2D tColor;
uniform sampler2D tDepth;
uniform sampler2D tNoise;

uniform vec2 uTexel;
uniform vec3 uPaper;
uniform vec3 uInk;

// 排线
uniform float uEngravingStrength; // 0..1
uniform float uHatchLayers; // 2..4
uniform float uHatchSpacing; // 屏幕像素
uniform float uHatchAngle; // 第一层角度（弧度）
uniform float uPosterizeLevels; // 3..5
uniform float uDensityGamma; // 调子曲线
uniform vec2 uToneRange; // 自动色调范围（点云亮度 5~95 分位）
uniform vec2 uScreenOffset; // 抖动换序（像素）

// 描边
uniform float uEdgeStrength;
uniform float uEdgeThreshold;
uniform float uEdgeWidth;
uniform vec2 uBoilJitter;
uniform float uBoilFlicker;

// 后期
uniform float uVignette;
uniform float uGrain;

varying vec2 vUv;

const vec3 LUMA = vec3(0.299, 0.587, 0.114);

float depthAt(vec2 uv) {
  return texture2D(tDepth, uv).r;
}

/** 一层排线的覆盖遮罩：沿 angle 方向、间距 spacing 的线条（1 = 有墨） */
float hatchLayer(vec2 p, float angle, float spacing) {
  float c = cos(angle);
  float s = sin(angle);
  float d = (c * p.x + s * p.y) / max(spacing, 1.0);
  float f = fract(d);
  // 线宽约占间距的 36%（经典排线的细线 + 白缝比例；再粗就会连成网格）
  float halfWidth = 0.18;
  float aa = max(fwidth(f), 0.02);
  return 1.0 - smoothstep(halfWidth - aa, halfWidth + aa, abs(f - 0.5));
}

void main() {
  vec3 color = texture2D(tColor, vUv).rgb;
  // 自动色调范围归一化（真实插画整体偏亮时，不归一化会全场没线）
  float lum = clamp(
    (dot(color, LUMA) - uToneRange.x) / max(uToneRange.y - uToneRange.x, 1e-3),
    0.0,
    1.0
  );

  // ── 调子 → posterize ──────────────────────────────────────────────────────
  float tone = pow(clamp(lum, 0.0, 1.0), uDensityGamma);
  float levels = clamp(uPosterizeLevels, 2.0, 6.0);
  float quantized = min(floor(tone * levels), levels - 1.0) / max(levels - 1.0, 1.0);

  // ── 3 层交叉排线：层数随暗度增加（越暗叠加越多层）───────────────────────
  vec2 p = gl_FragCoord.xy + uScreenOffset;
  float ink = 0.0;
  float layers = clamp(uHatchLayers, 1.0, 4.0);

  // 第 1 层：中间调就开始（阈值 0.86）
  ink = max(ink, hatchLayer(p, uHatchAngle, uHatchSpacing) * step(quantized, 0.90));
  // 第 2 层：更暗的档（阈值 0.62），角度 +45°，线更密一点
  if (layers >= 2.0) {
    ink = max(
      ink,
      hatchLayer(p, uHatchAngle + 0.7854, uHatchSpacing * 0.92) * step(quantized, 0.62)
    );
  }
  // 第 3 层：暗部（阈值 0.38），角度 +90°
  if (layers >= 3.0) {
    ink = max(
      ink,
      hatchLayer(p, uHatchAngle + 1.5708, uHatchSpacing * 0.84) * step(quantized, 0.35)
    );
  }
  // 第 4 层（可选）：最暗处，角度 +135°
  if (layers >= 4.0) {
    ink = max(
      ink,
      hatchLayer(p, uHatchAngle + 2.3562, uHatchSpacing * 0.76) * step(quantized, 0.2)
    );
  }
  // 最暗一档压成实墨（版画的"暗部实心"）
  ink = max(ink, step(quantized, 0.06));

  // 只对「有点云的地方」上墨：背景保持纯纸色
  float coverage = 1.0 - smoothstep(0.97, 0.997, depthAt(vUv));
  vec3 engraved = mix(uPaper, uInk, ink * coverage);
  vec3 outCol = mix(color, engraved, clamp(uEngravingStrength, 0.0, 1.0));

  // ── 轮廓线：sobel on 平滑深度（铜版画的刻线）──────────────────────────────
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
  vec2 v = vUv - 0.5;
  outCol *= 1.0 - uVignette * smoothstep(0.20, 0.80, length(v));
  float grain = texture2D(tNoise, gl_FragCoord.xy / 256.0).r - 0.5;
  outCol += grain * uGrain;

  gl_FragColor = vec4(outCol, 1.0);
}
