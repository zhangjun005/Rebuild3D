# testdata

开发用的固定输入。**这里是"可复现"的锚点**——前端、Go、Python 三条线都对着同一份数据开发。

## sample.ilcl —— 合成点云 fixture

118,401 点 / 1,776,023 字节，ILC1 格式（见 `docs/selection.md` §4.2）。

场景是**特意设计**的，让每一种风格化手段都有可验证的输入：

| 部件 | 位置 | 用来验证 |
|---|---|---|
| 地面 | `y = -1`，约 4.6 万点，远处渐暗 + 棋盘 | 空气透视、排线方向 |
| 球体 | 中心 `(0, 0.35, 0)`，半径 0.85，约 4.2 万点，受光渐变 | 曲率、法线、**点画的密度调制** |
| 立方体 | 中心 `(1.25, 0.15, -0.9)`，边长 1.4，六面各一色相 | **硬边深度描边** |

重新生成：

```bash
go run testdata/gen/main.go
```

生成器在 `testdata/gen/main.go`（带 `//go:build ignore`，不参与 `go build ./...`）。

## illustrations/ —— 插画输入（不进版本库）

**你自己丢图进来**。这个目录已 gitignore（体积 + 版权）。

go/no-go 探针（`scripts/probe-depth.py`）会读这里。理想情况下放 3 张**风格差异大**的：

1. **日式平涂**（anime 背景美术）—— 大面积纯色块、清晰分区
2. **厚涂 / 有明确光影** —— 检验深度模型能不能吃到明暗线索
3. **线稿或黑白漫画** —— 最难：平涂、无光照、自带网点噪声

当前已有的样本（从旧工程 `code/IOPaint-main/assets/` 借的）：

- `sample-manga.png` — 黑白漫画分镜（网点 + 白描 + 文字气泡）。这是**最难**的用例，
  如果它能出可用深度，说明模型对平涂插画的泛化不错；如果它失败，也不能直接判死刑。

运行探针：

```bash
conda activate illust3d
python scripts/probe-depth.py testdata/illustrations
# 输出到 out/probe/，重点看 *.depth_color.png
```
