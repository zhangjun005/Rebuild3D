// 全屏三角/四边形的 vertex：直接输出裁剪空间坐标（PlaneGeometry(2,2) 的 xy ∈ [-1,1]）。

varying vec2 vUv;

void main() {
  vUv = uv;
  gl_Position = vec4(position.xy, 0.0, 1.0);
}
