<!-- kron:frontmatter -->
id: world-space-halftone-is-next
text: 点云的下一步渲染升级应该是「世界空间网点」（图案贴在表面上），而不是继续调屏幕空间网点。
default_severity: soft
status: draft
created_by: '@zhangjun005'
updated_at: "2026-11-11T05:20:38Z"
expires_at: "2026-12-31"
<!-- /kron:frontmatter -->

## 这是草案，尚未验证

现状：半调网点、铜版排线都在**屏幕空间**合成 pass 里做（见 `render/style-layering/composite`）。
好处是图案稳定、实现简单；代价是转动时图案**不跟着表面走**——眼睛能看出来"这是贴在镜头上的"。

## 假设

把网点做到**世界空间**（图案随点的切平面走）观感会明显更像"真的印在物体上"，
值得为它引入每点切向（`render/methods-inventory` 里的 A6/G7）。

## 怎么验证

1. 先只在一个预设里做世界空间网点（用现有的深度图算切平面）
2. 同一角度出两帧：屏幕空间 vs 世界空间，对比"转动时图案是否跟着表面"
3. 若观感提升不明显，就放弃这条，别为它引入新属性

## 未验证的部分

- 每点切向的估计开销（Go 侧从结构图算，还是 shader 里从深度算）
- 低点密度时世界空间网点会不会闪（时间稳定性）
