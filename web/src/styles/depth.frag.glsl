// 深度 pass 的 fragment：把归一化线性深度写进颜色通道（HalfFloat target 保精度）。
// 背景由 clear 色给出 1.0（最远），所以轮廓总是相对背景有台阶。
//
// ★ 必须做圆形 discard。
//   depth.vert.glsl 为了封住点间隙把点放大了（uGrow），如果这里不裁成圆盘，
//   深度图里每个点就是一个**方形 tile**；曲面上相邻点深度不同，方形 tile 的边界
//   就变成一圈 1~2 px 的假深度台阶，被 post-edge 的 Laplacian 当作棱线描出来——
//   结果是每个点被描上一圈方框。这是「点变成方块 + 深色边框」的根因。
//
// 拿掉方框的因果链：depth pass 圆盘化 → 深度图没有 tile 边界 → 描边只认真实台阶。

varying float vDepth;

void main() {
  if (length(gl_PointCoord - 0.5) > 0.5) discard;
  gl_FragColor = vec4(vDepth, vDepth, vDepth, 1.0);
}
