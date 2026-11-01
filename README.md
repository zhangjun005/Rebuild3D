# 插画 → 点云 → 风格化渲染

一张插画进去，出来一个能转、能动、有画风的东西。目标"好看"，不追求"准确"。

## 文档（先读这个）

| 文档 | 内容 |
|---|---|
| [docs/README.md](docs/README.md) | 索引、当前主线、环境与硬件事实 |
| [docs/scope.md](docs/scope.md) | 主线定义、技术方案、分阶段（P0~P6）、验证顺序 |
| [docs/selection.md](docs/selection.md) | **★ 施工依据**：conda 隔离、模型选型、仓库结构、接口协议、资源预算 |
| [docs/rendering-styles.md](docs/rendering-styles.md) | **★ 风格渲染清单**：三层结构、7 种风格、6 套预设、参数表 |
| [docs/go-scope.md](docs/go-scope.md) | Go 的职责边界与技术选型 |
| [docs/archive/](docs/archive/README.md) | 历史思考（旧工程盘点、业务重估、空间/户型方向） |

意图与假设记录在 `.kron/`（用 kron 管理）。

## 结构

```
cmd/illust3d/        Go 入口（单二进制）
internal/
  api/               路由、静态资源、SSE
  infer/             sidecar 客户端（stdio 帧协议）
  geometry/          反投影、边缘剔除、重采样、法线
  cloud/             点云内部表示 + ILC1 二进制编解码
  exporters/         PLY、自包含 HTML
  config/            配置与风格预设
sidecar/             Python 常驻子进程（只做推理）
web/                 three.js + GLSL 风格 shader
scripts/             环境与构建脚本
testdata/            开发用固定点云，供前端离线开发
docs/                决策与规范
```

## 状态

骨架已就位，业务逻辑尚未实现。下一步是 [docs/scope.md](docs/scope.md) 的第 1 项验证：
**拿几张插画跑 DA3，看深度能不能用**——这是整条线的 go/no-go。

## 三条铁律

1. 只在 conda env（`illust3d`）里装东西，绝不碰全局
2. 所有缓存与模型都落 E 盘（C 盘只剩 ~3.9 GB）
3. `code/` 旧工程仅作参考，不作为依赖
