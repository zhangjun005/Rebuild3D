<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T13:58:01.5787064Z
<!-- /kron:frontmatter -->
# 散落点清理（总）

> 只压线稿缝合处的伪影，不压真实台阶。

## 这个节点（总）

总决策：**先搞清散落点在哪**。热图证明它们描在插画的每根线稿上（而不是人物外轮廓），所以 ①不能用语义轮廓划边界；②必须区分"线稿缝合伪影"与"真实几何台阶"（如手臂朝前）。两条路线里一条已否决、一条待验证。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `geometry/scatter-cleanup/seam-compression` | 现行：按深度不连续 + 同层 IQR 判据（默认关） |
| `geometry/scatter-cleanup/layer-quantization` | 已否决：按连通域整块压平，会丢立体感 |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
