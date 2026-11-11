<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-11T05:19:32.7305683Z
status: draft
assumptions:
    - id: world-space-halftone-is-next
      severity: soft
      rationale: 整条草案的成立与否都压在这个未验证的观感判断上；如果它不成立，就不该为此引入每点切向属性。
<!-- /kron:frontmatter -->
# 世界空间网点（草案）

> 现状：半调网点与铜版排线都在**屏幕空间**合成 pass 里做，图案稳定但转动时**不跟着表面走**，眼睛能看出"贴在镜头上"。

**草案（draft），尚未实现。** 现状：半调网点与铜版排线都在**屏幕空间**合成 pass 里做，图案稳定但转动时**不跟着表面走**，眼睛能看出"贴在镜头上"。假设：改到世界空间（图案随点的切平面走）会明显更像真的印在物体上。验证方式是用同一角度出两帧对比"转动时图案是否跟着表面"。依赖 `render/style-layering/primitive-layer` 引出的每点切向能力（见 `render/methods-inventory` 的 A6/G7）。
