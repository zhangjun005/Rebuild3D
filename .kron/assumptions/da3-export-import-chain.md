<!-- kron:frontmatter -->
id: da3-export-import-chain
text: depth_anything_3/api.py 顶层 import 了 utils.export，而该模块又顶层 import gs/colmap/glb，因此只做深度推理也会被拖入 trimesh/open3d/pycolmap/moviepy/gsplat，必须打一行懒加载补丁。
default_severity: hard
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-01T08:48:06Z"
verified_at: "2026-11-01"
verified_by: '@zhangjun005'
<!-- /kron:frontmatter -->

## 依据（已读上游源码确认）

- `src/depth_anything_3/api.py` 顶层有 `from depth_anything_3.utils.export import export`
- `src/depth_anything_3/utils/export/__init__.py` 顶层 import 了 `gs` / `colmap` / `depth_vis` / `feat_vis` / `glb` / `npz`

所以 `import depth_anything_3.api` 会把整条导出链拉起来，只做深度推理也会被拖入
trimesh / open3d / pycolmap / moviepy / gsplat。

## 应对

不改上游语义，只做一行本地补丁：删掉 api.py 顶层那行 import，改为在 `_export_results()` 内部懒加载。
补丁记在 `sidecar/vendor/PATCHES.md`，升级 DA3 时重新打。
