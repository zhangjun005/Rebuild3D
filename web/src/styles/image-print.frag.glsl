// ─────────────────────────────────────────────────────────────────────────────
// 图像模式（image mode）合成 pass：把「原图」做成墨/纸风格。
//
// 与点云模式的合成 pass 是**同一套外观参数**（纸色/墨色/网点/排线/描边/暗角/颗粒），
// 区别只有两点：
//   1. 输入是连续图像（不是点云渲染结果）→ 线稿/五官/装饰天然保留
//   2. 描边从**图像亮度**的 sobel 得到（点云模式是深度 sobel）→ 线稿会被描出来
//
// 三种画风（uStyle）：0 = 屏幕空间点画（blue noise） / 1 = 网点半调 / 2 = 交叉排线。
// 图像模式下点画用**屏幕空间 blue noise** 是安全的：blue noise 打规则点阵会摩尔纹，
// 打连续图像不会（采样定理问题，见 §8）。
//
// 墨量映射（uInkSource，两种模式共用同一套语义）：
//   0 = 按亮度（传统：暗处上墨，适合深色主体）
//   1 = 按局部结构（默认：局部梯度大才上墨 → 平坦区留纸、五官/褶皱/线稿上墨）
//   2 = 按深度（强调层次；图像模式没有深度 → 退回亮度）
// 主体极性（uInkPolarity）：0 自动（整幅偏亮 → 视为亮主体）/ 1 亮主体 / 2 暗主体。
// ─────────────────────────────────────────────────────────────────────────────

uniform sampler2D tColor;
uniform sampler2D tNoise;

uniform vec2 uTexel;
uniform vec3 uPaper;
uniform vec3 uInk;

// 画风选择与强度
uniform float uStyle; // 0 点画 / 1 半调 / 2 排线
uniform float uMono; // 0 = 保留图像颜色，1 = 墨/纸 duotone
uniform float uDensityGamma; // 明暗曲线（>1 对比更强）
uniform float uStippleStrength;
uniform float uStippleScale; // 点画网点尺度（像素）
uniform float uHalftoneStrength;
uniform float uHalftoneCell;
uniform float uHalftoneAngle;
uniform float uHatchStrength;
uniform float uHatchLayers;
uniform float uHatchSpacing;
uniform float uHatchAngle;
uniform vec2 uScreenOffset; // 沸腾：网屏平移（像素）

// 墨量映射
uniform float uInkSource;
uniform float uInkPolarity;
uniform float uInkGain;
uniform float uMeanLuminance;

// 描边（图像亮度 sobel）
uniform float uEdgeStrength;
uniform float uEdgeThreshold;
uniform float uEdgeScale; // 图像边缘梯度尺度与深度梯度不同 → 阈值缩放
uniform float uEdgeWidth;
uniform vec2 uBoilJitter;
uniform float uBoilFlicker;

// 后期
uniform float uVignette;
uniform float uGrain;

varying vec2 vUv;

const vec3 LUMA = vec3(0.299, 0.587, 0.114);

float lumaAt(vec2 uv) {
  return dot(texture2D(tColor, uv).rgb, LUMA);
}

/** 一层排线遮罩（与铜版画同一实现：线宽 ≈ 间距 36%） */
float hatchLayer(vec2 p, float angle, float spacing) {
  float c = cos(angle);
  float s = sin(angle);
  float d = (c * p.x + s * p.y) / max(spacing, 1.0);
  float f = fract(d);
  float halfWidth = 0.18;
  float aa = max(fwidth(f), 0.02);
  return 1.0 - smoothstep(halfWidth - aa, halfWidth + aa, abs(f - 0.5));
}

void main() {
  vec3 color = texture2D(tColor, vUv).rgb;
  float lum = lumaAt(vUv);

  // ── 墨量 coverage ────────────────────────────────────────────────────────
  float tone;
  if (uInkSource < 0.5) {
    tone = lum;
  } else if (uInkSource < 1.5) {
    // 局部结构：3×3 亮度梯度的幅值 → 结构越强越上墨
    vec2 e = uTexel * 1.5;
    float gx = lumaAt(vUv + vec2(e.x, 0.0)) - lumaAt(vUv - vec2(e.x, 0.0));
    float gy = lumaAt(vUv + vec2(0.0, e.y)) - lumaAt(vUv - vec2(0.0, e.y));
    float structure = clamp(length(vec2(gx, gy)) * uInkGain, 0.0, 1.0);
    tone = 1.0 - structure;
  } else {
    tone = lum; // 图像模式无深度：退回亮度
  }

  // 主体极性
  float flip = 0.0;
  if (uInkPolarity > 1.5) {
    flip = 0.0; // 暗主体：传统映射（暗处上墨）
  } else if (uInkPolarity > 0.5) {
    flip = 1.0; // 亮主体：亮处上墨
  } else {
    flip = step(0.62, uMeanLuminance); // 自动：整幅偏亮 → 当亮主体处理
  }
  // 结构来源不翻转：结构本身就是「该上墨的地方」，翻转会变成给平坦区上墨
  if (flip > 0.5 && (uInkSource < 0.5 || uInkSource > 1.5)) tone = 1.0 - tone;

  float coverage = pow(clamp(1.0 - tone, 0.0, 1.0), max(uDensityGamma, 0.1));

  // ── 画风 ─────────────────────────────────────────────────────────────────
  float ink = 0.0;
  if (uStyle < 0.5) {
    // 屏幕空间点画：blue noise 阈值（输入是连续图像 → 不会有规则网格摩尔纹）
    vec2 np = (gl_FragCoord.xy + uScreenOffset) / (256.0 * max(uStippleScale, 0.5));
    float n = texture2D(tNoise, np).r;
    ink = smoothstep(n - 0.07, n + 0.07, coverage) * uStippleStrength;
  } else if (uStyle < 1.5) {
    // 半调网点（半径系数 0.5，印刷几何约束）
    vec2 shifted = gl_FragCoord.xy + uScreenOffset;
    float c = cos(uHalftoneAngle);
    float s = sin(uHalftoneAngle);
    vec2 rotated = vec2(c * shifted.x - s * shifted.y, s * shifted.x + c * shifted.y);
    vec2 cell = fract(rotated / max(uHalftoneCell, 2.0)) - 0.5;
    float radius = sqrt(clamp(coverage, 0.0, 1.0)) * 0.5;
    float dots = 1.0 - smoothstep(radius - 0.07, radius + 0.07, length(cell));
    ink = dots * uHalftoneStrength;
  } else {
    // 交叉排线：层数随墨量增加
    vec2 p = gl_FragCoord.xy + uScreenOffset;
    float spacing = max(uHatchSpacing, 2.0);
    float layers = clamp(uHatchLayers, 1.0, 4.0);
    float hatch = 0.0;
    for (int i = 0; i < 4; i++) {
      float fi = float(i);
      if (fi >= layers) break;
      float amount = clamp(coverage * layers - fi, 0.0, 1.0);
      hatch = max(hatch, hatchLayer(p, uHatchAngle + fi * 0.7854, spacing) * step(0.02, amount));
      spacing *= 0.92;
    }
    hatch = max(hatch, step(0.92, coverage)); // 最暗处压成实墨
    ink = hatch * uHatchStrength;
  }

  // duotone：mono=1 时整幅只剩墨/纸两色；mono=0 保留图像颜色（调试用）
  vec3 outCol = mix(color, mix(uPaper, uInk, ink), clamp(uMono, 0.0, 1.0));

  // ── 描边：sobel on 图像亮度（线稿在这里被描出来）─────────────────────────
  vec2 uv = vUv + uBoilJitter * uTexel;
  vec2 o = uTexel * max(uEdgeWidth, 1.0);

  float tl = lumaAt(uv + vec2(-o.x, o.y));
  float tc = lumaAt(uv + vec2(0.0, o.y));
  float tr = lumaAt(uv + vec2(o.x, o.y));
  float ml = lumaAt(uv + vec2(-o.x, 0.0));
  float mr = lumaAt(uv + vec2(o.x, 0.0));
  float bl = lumaAt(uv + vec2(-o.x, -o.y));
  float bc = lumaAt(uv + vec2(0.0, -o.y));
  float br = lumaAt(uv + vec2(o.x, -o.y));

  float gx = (tr + 2.0 * mr + br) - (tl + 2.0 * ml + bl);
  float gy = (bl + 2.0 * bc + br) - (tl + 2.0 * tc + tr);
  float gradient = length(vec2(gx, gy));

  float thr = max(uEdgeThreshold * uEdgeScale, 1e-4) * (1.0 + 0.45 * uBoilFlicker);
  float edge = smoothstep(thr, thr * 1.8 + 1e-5, gradient);
  outCol = mix(outCol, uInk, clamp(edge * uEdgeStrength, 0.0, 1.0));

  // ── 暗角 + 纸颗粒 ────────────────────────────────────────────────────────
  vec2 p = vUv - 0.5;
  outCol *= 1.0 - uVignette * smoothstep(0.20, 0.80, length(p));
  float grain = texture2D(tNoise, gl_FragCoord.xy / 256.0).r - 0.5;
  outCol += grain * uGrain;

  gl_FragColor = vec4(outCol, 1.0);
}
