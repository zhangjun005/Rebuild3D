// ─────────────────────────────────────────────────────────────────────────────
// 图像模式（image mode）第一 pass：把上传的原图贴到全屏 quad。
//
// 为什么要有图像模式：点画/半调/排线**本质上是 2D 图像处理**，点云只贡献「立体感/视差」。
// 为了立体感，点云模式付出的是深度分辨率的信息损失（4000×4000 → 504×504），线稿全丢。
// 图像模式完全不碰深度、不请求 Go：原图直接当纹理，然后复用同一套合成 pass。
//
// 摆放规则 = contain：整幅图完整可见，四周留纸色（插画的边缘就是构图，不能裁）。
// ─────────────────────────────────────────────────────────────────────────────

uniform sampler2D tImage;
/** 图像占屏幕宽/高的比例（contain）；图像窗口外的像素 = 纸色 */
uniform vec2 uImageScale;
uniform vec3 uPaper;

varying vec2 vUv;

void main() {
  // 屏幕 vUv.y 向上、纹理行序向下：这里显式翻一次 Y（ImageBitmap 的 flipY 在部分实现里不生效）
  vec2 uv = (vec2(vUv.x, 1.0 - vUv.y) - 0.5) / max(uImageScale, vec2(1e-4)) + 0.5;
  vec3 color = uPaper;
  if (uv.x >= 0.0 && uv.x <= 1.0 && uv.y >= 0.0 && uv.y <= 1.0) {
    color = texture2D(tImage, uv).rgb;
  }
  gl_FragColor = vec4(color, 1.0);
}
