<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T10:44:33.1379326Z
status: active
assumptions:
    - id: depth-on-illustration-usable
      severity: hard
      rationale: 若插画深度不可用就要整体退回纯 2.5D 分层视差，技术方案与阶段计划全改
      verified_at: "2026-11-01"
      verified_by: '@zhangjun005'
    - id: illustration-depth-figure-ground-only
      severity: hard
      rationale: 决定能不能做自由环绕；忘记这条就会做出能转 360 度的界面，一转就穿帮
      verified_at: "2026-11-01"
      verified_by: '@zhangjun005'
    - id: da3mono-no-confidence-map
      severity: hard
      rationale: 过滤策略得整套换掉，否则被误判的出格点会直接进点云，再被风格化放大成脏点
      verified_at: "2026-11-01"
      verified_by: '@zhangjun005'
    - id: da3-mono-fits-8gb
      severity: hard
      rationale: 峰值显存超 6GB 就得上降分辨率或降模型档，直接影响画质目标
      verified_at: "2026-11-01"
      verified_by: '@zhangjun005'
depends_on:
    - product/illustration-pointcloud
    - geometry/depth-model-choice
<!-- /kron:frontmatter -->
# go/no-go 结果：条件性通过

> 2026-11-01，RTX 4060 Laptop 8GB，DA3MONO-LARGE @ process_res=504，7 张输入
> （5 张风格化插画 + 1 张真人照片 + 1 张黑白漫画）

## 结论

**通过，但产品形态要改：从「可环绕的 3D 场景」退到「小幅视差的 2.5D 点画动画」。**

## 实测数据

| 指标 | 结果 | 对目标 |
|---|---|---|
| 峰值显存 | **2157 MiB** | ✅ 目标 < 6144 MiB，余量很大 |
| 单图耗时 | **0.15 ~ 0.23 s** | ✅ 远好于预期 |
| NaN 比例 | 0% | ✅ |
| `conf` 置信度 | ❌ **没有** | ⚠️ 见下 |
| `intrinsics` | ❌ 没有 | ✅ 不影响（焦距是艺术参数） |

## 深度质量：不是"纸片"，是"剪影"

| 输入 | 质量 | 观察 |
|---|---|---|
| 真人照片 | ✅ 好 | 帽子/手臂/手的前后关系都能恢复（在训练分布内） |
| 厚涂（有光影） | ⚠️ 剪影 | 主体与背景分层清楚，**主体内部是平的** |
| 平涂（动漫） | ⚠️ 剪影 | 主体几乎是一块等深度的板 |
| 黑白漫画 | ❌ 差 | 只剩一团，无分层（最难用例） |

**核心认知：插画深度给的是「前景/背景分层」，不是「主体内部体积」。**

## 因此产品需要改三点

1. **相机角度限制在 ±12~18°**
   不要做 360° 环绕——主体是平板，转过去露边缘就穿帮。
   交互从"环绕"改成"摆动/视差"。

2. **置信度过滤改为深度梯度过滤**
   没有 `conf`，`geometry.FilterDepthEdges`（已实现）正好顶上。

3. **背景延展变成关键一步**
   背景深度平坦、占画面比例大，直接延展即可，不需要上 LDI inpainting。
   这反而比原计划简单。

## 没变的部分

**点云风格渲染本身完全成立。** 点云的"厚度感"主要来自点大小、密度、描边、空气透视，
不是几何精度——而这一层已经验证过了（见 `render/style-layering`、`render/density-as-style`）。

## 下一步

1. Python sidecar：把这套逻辑封装成常驻进程（帧协议按 `arch/three-way-split`）
2. Go 侧接上：深度 → 梯度过滤 → 反投影 → ILC1
3. Web 侧：相机角度限位 + 背景延展
4. 相机限位与背景延展要写进 `docs/scope.md` 的阶段计划
