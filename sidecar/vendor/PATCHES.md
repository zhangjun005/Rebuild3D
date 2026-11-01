# vendor 补丁记录

## 1. src/depth_anything_3/api.py —— export 改懒加载

**为什么**
上游 pi.py 顶层写了 rom depth_anything_3.utils.export import export，
而 utils/export/__init__.py 又顶层 import gs / colmap / depth_vis / eat_vis / glb / 
pz。
结果是：只做深度推理也会被拖入 trimesh / open3d / pycolmap / moviepy / gsplat 一整串依赖。

**怎么改（两处文本替换）**
1. 删除顶层 rom depth_anything_3.utils.export import export
2. 在调用点 export(prediction, export_format, export_dir, **kwargs) 之前插入同一行 import

**升级 DA3 时**
删除 sidecar/vendor/Depth-Anything-3，重跑 scripts/setup-env.ps1。
若上游结构变化，脚本会因找不到匹配文本而报错停下——**这是有意的**，避免静默打错补丁。
