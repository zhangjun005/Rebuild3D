<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:21.7779623Z
status: active
assumptions:
    - id: da3-mono-fits-8gb
      severity: hard
      rationale: 一旦破裂就只能降分辨率或降档，直接影响画质目标与端到端耗时
    - id: xformers-optional
      severity: hard
      rationale: 破裂后要在 Windows 上冒险改动底层张量库版本，可能连带毁掉整个 CUDA 环境
      verified_at: "2026-11-01"
      verified_by: '@zhangjun005'
    - id: da3-export-import-chain
      severity: hard
      rationale: 若判断有误，按上游默认方式安装会把一整串图形与导出依赖塞进环境，甚至直接导入失败
      verified_at: "2026-11-01"
      verified_by: '@zhangjun005'
depends_on:
    - arch/three-way-split
<!-- /kron:frontmatter -->
# 深度模型选 DA3MONO-LARGE

> > 单图场景用 `DA3MONO-LARGE`（0.35B，Apache-2.0），备选 `DA3-SMALL`，以及带天空分割的 `DA3ME…

TRIC-LARGE`。

## 为什么（Why）

**单图插画用不上多视图优势。** DA3 主系列（SMALL/BASE/LARGE）的核心价值是"多视角一致"，
单图输入时这个能力退化，而专门做单目的模型更对口：

- `DA3MONO-LARGE` 直接预测 depth（而非 disparity），官方明确说这对几何精度更好
- 许可证是 **Apache-2.0**，可商用
- 0.35B 在 8GB 上余量充足

`DA3-SMALL`（0.08B，同样 Apache-2.0）留作快速验证和低配降级。

`DA3METRIC-LARGE`（0.35B，Apache-2.0）输出米制——插画不需要米制，但它**带 sky segmentation**，
正好能用来抠背景（背景延展那一步用得上），值得单独测一次。

**明确排除** `DA3-LARGE` / `DA3-GIANT` / `DA3NESTED-*`：1.15B/1.40B 在 8GB 上装不下，
且是 **CC BY-NC 4.0 非商用**。

## 如何（How）

- 源码 **vendor，不 `pip install -e .`**：固定 commit clone 到 `sidecar/vendor/`，规避 pyproject 里
  trimesh / open3d / pycolmap / evo / moviepy / gsplat 那一长串
- 打一行本地补丁：把 `api.py` 顶层的 `from ...utils.export import export` 改成 `_export_results()` 内懒加载
  （依据见假设 `da3-export-import-chain`）
- **不装 xformers**（依据见假设 `xformers-optional`）
- 模型下载到 `HF_HOME=E:\models\hf`

## 权衡（Tradeoffs）

- 放弃多视图一致性：补洞只能靠 LDI inpainting，不能靠多视角融合
- 放弃 GS head：本来也在 CC BY-NC 的 GIANT 上，用不了
- 依赖上游仓库结构，升级时要重新打补丁（补丁只有一处，风险可控）
- `conf` 置信度图是否存在尚未确认——官方文档只写 "optional, depends on model"

## 备选（Alternatives）

- `DA3-SMALL`：更省，但质量未知，需对比
- `DA3METRIC-LARGE`：为了它的 sky segmentation，可作背景分割的备选
