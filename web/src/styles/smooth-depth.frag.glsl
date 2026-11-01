// ─────────────────────────────────────────────────────────────────────────────
// 深度清理：5×5 最小值填充（补齐点的空隙） + 双边平滑
//
// §9 的坑：「深度图精度不足会闪烁，先平滑深度再求梯度」。
// 但朴素 gaussian 平滑会把轮廓（前后景的深度台阶）糊掉，描边就没了。
// 所以分两步：
//   1. 5×5 最小值（= 最近的深度）当参考中心 —— 点云的洞（点与点之间的空白）会被最近的
//      前景点填上；轮廓不会被点阵打成一串珠子（scalloping），曲面上的小区块阶梯也被抹平。
//   2. 再在「与参考深度相近」的邻域里平均（双边权重），轮廓两侧互不干扰。
//
// 为什么是 5×5 而不是 3×3：3×3 只够填 1 px 的洞；放大过的点（uGrow=3）在曲面上的
// 相邻深度差造成的伪台阶需要更大半径才能压平。uRangeTol 的语义不变
// （归一化深度差超过它就不参与平均），所以真正的轮廓台阶仍然保得住。
// ─────────────────────────────────────────────────────────────────────────────

uniform sampler2D tDepth;
uniform vec2 uTexel;
uniform float uRangeTol; // 归一化深度差超过它就不参与平均

varying vec2 vUv;

float tap(vec2 offset) {
  return texture2D(tDepth, vUv + offset * uTexel).r;
}

/** 双边权重：深度越接近参考值权重越大 */
float weight(float s, float center) {
  return 1.0 - smoothstep(0.0, uRangeTol, abs(s - center));
}

void main() {
  // ── 1. 填洞：只有当中心比邻域最小值「明显更远」时，才认为它是点云的空隙
  //        （深度 pass 只画点，点与点之间没有覆盖 → 那些像素留着背景深度 1.0）。
  //        条件替换而不是无条件取最小值，是为了不把立方体顶面那种几像素高的
  //        近侧视窄条整条吃掉。
  float center = texture2D(tDepth, vUv).r;
  float nearest = center;
  for (int dy = -2; dy <= 2; dy++) {
    for (int dx = -2; dx <= 2; dx++) {
      if (dx == 0 && dy == 0) continue;
      nearest = min(nearest, tap(vec2(float(dx), float(dy))));
    }
  }
  float ref = center > nearest + uRangeTol ? nearest : center;

  // ── 2. 以 ref 为参考的双边平均（5×5）：曲面上的小区块阶梯被同深度邻域平滑掉，
  //        真正的轮廓台阶（深度差 >> uRangeTol）不被跨越。
  float sum = ref;
  float wsum = 1.0;
  for (int dy = -2; dy <= 2; dy++) {
    for (int dx = -2; dx <= 2; dx++) {
      if (dx == 0 && dy == 0) continue;
      float s = tap(vec2(float(dx), float(dy)));
      float w = weight(s, ref);
      sum += s * w;
      wsum += w;
    }
  }

  float depth = sum / wsum;
  gl_FragColor = vec4(depth, depth, depth, 1.0);
}
