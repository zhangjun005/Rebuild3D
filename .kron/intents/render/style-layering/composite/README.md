<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:59:18.3475735Z
status: active
<!-- /kron:frontmatter -->
# 合成 pass（总）

> > 印刷质感在合成 pass 做，不在点里做。


## 这个节点（总）

总决策：**图案放屏幕空间**。如果按点着色，网点会跟着点走，转动时图案在物体上滑动（错）；放屏幕空间后图案固定在画面上，物体在图案下移动——这才是印刷的观感。代价是它不是"贴在世界表面上"的网点（那项未做）。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `render/style-layering/composite/halftone-screen` | 屏幕空间半调网点 |
| `render/style-layering/composite/engraving-hatch` | 铜版交叉排线（与网点互斥） |
| `render/style-layering/composite/post-edge` | 深度 Sobel 描边 |
| `render/style-layering/composite/world-space-halftone` | 世界空间网点（draft，未实现） |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
