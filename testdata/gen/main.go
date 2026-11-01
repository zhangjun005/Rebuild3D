//go:build ignore

// 生成 testdata/sample.ilcl —— 一个合成点云，供前端在没有后端时独立开发。
//
// 用法（在仓库根目录）：
//
//	go run testdata/gen/main.go
//
// 场景设计目的：让每一种风格手段都有可验证的输入
//   - 球体：连续的曲率变化 → 检验深度衰减、法线、点画密度
//   - 立方体：硬边 → 检验深度描边
//   - 地面：大片同向平面 → 检验空气透视与排线方向
//
// 输出格式即 docs/selection.md §4.2 的内部传输格式（ILC1，全小端）：
//
//	magic "ILC1"  4 B
//	count uint32  4 B
//	pos   float32[3*count]
//	col   u8[3*count]
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
)

type pt struct{ x, y, z, r, g, b float32 }

func main() {
	rng := rand.New(rand.NewSource(20261101))
	var pts []pt

	// ── 地面：y = -1.0，x/z ∈ [-2.2, 2.2]，抖动网格 ──
	const step = 0.022
	for x := -2.2; x <= 2.2; x += step {
		for z := -2.2; z <= 2.2; z += step {
			jx := x + (rng.Float64()-0.5)*step*0.8
			jz := z + (rng.Float64()-0.5)*step*0.8
			// 越远越暗，形成自然的深度渐变，便于检验空气透视
			d := math.Sqrt(jx*jx + jz*jz)
			shade := 0.75 - 0.10*d
			if shade < 0.30 {
				shade = 0.30
			}
			// 轻微棋盘，便于看清透视
			if (int(math.Floor(x/0.4))+int(math.Floor(z/0.4)))%2 == 0 {
				shade *= 0.86
			}
			c := float32(shade)
			pts = append(pts, pt{float32(jx), -1.0, float32(jz), c * 0.62, c * 0.66, c * 0.72})
		}
	}

	// ── 球体：球心 (0, 0.35, 0)，半径 0.85，斐波那契球面 ──
	const nSphere = 42000
	for i := 0; i < nSphere; i++ {
		t := float64(i) + 0.5
		phi := math.Acos(1 - 2*t/nSphere)
		theta := math.Pi * (1 + math.Sqrt(5)) * t
		nx := math.Sin(phi) * math.Cos(theta)
		ny := math.Cos(phi)
		nz := math.Sin(phi) * math.Sin(theta)
		const r = 0.85
		x, y, z := nx*r, 0.35+ny*r, nz*r
		// 用"受光"做颜色：左上方向来光，产生明确的明暗过渡
		lum := 0.30 + 0.70*math.Max(0, -0.45*nx+0.78*ny-0.44*nz)
		c := float32(math.Min(1, lum))
		pts = append(pts, pt{float32(x), float32(y), float32(z),
			c * 0.95, c * 0.78, c * 0.55})
	}

	// ── 立方体：中心 (1.25, 0.15, -0.9)，边长 0.7，六面采样 ──
	const cubeC = 0.5
	const nFace = 6000
	cx, cy, cz := 1.25, 0.15, -0.9
	for axis := 0; axis < 3; axis++ {
		for side := -1.0; side <= 1.0; side += 2.0 {
			for i := 0; i < nFace; i++ {
				u := (rng.Float64()*2 - 1) * cubeC
				v := (rng.Float64()*2 - 1) * cubeC
				var x, y, z float64
				switch axis {
				case 0:
					x, y, z = side*cubeC, u, v
				case 1:
					x, y, z = u, side*cubeC, v
				default:
					x, y, z = u, v, side*cubeC
				}
				// 每个面一个明显的色相，便于肉眼确认面朝向与描边
				var r, g, b float64
				switch axis {
				case 0:
					r, g, b = 0.85, 0.35, 0.30
				case 1:
					r, g, b = 0.35, 0.80, 0.45
				default:
					r, g, b = 0.35, 0.50, 0.90
				}
				k := 0.75 + 0.25*side
				pts = append(pts, pt{float32(cx + x), float32(cy + y), float32(cz + z),
					float32(r * k), float32(g * k), float32(b * k)})
			}
		}
	}

	// ── 写出 ILC1 ──
	out := filepath.Join("testdata", "sample.ilcl")
	f, err := os.Create(out)
	if err != nil {
		fail(err)
	}
	defer f.Close()

	w := func(v any) {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			fail(err)
		}
	}

	w([]byte("ILC1"))
	w(uint32(len(pts)))
	for _, p := range pts {
		w(p.x)
		w(p.y)
		w(p.z)
	}
	for _, p := range pts {
		w(uint8(p.r * 255))
		w(uint8(p.g * 255))
		w(uint8(p.b * 255))
	}

	// 统计包围盒，方便前端自检
	minX, minY, minZ := math.Inf(1), math.Inf(1), math.Inf(1)
	maxX, maxY, maxZ := math.Inf(-1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minX = math.Min(minX, float64(p.x))
		minY = math.Min(minY, float64(p.y))
		minZ = math.Min(minZ, float64(p.z))
		maxX = math.Max(maxX, float64(p.x))
		maxY = math.Max(maxY, float64(p.y))
		maxZ = math.Max(maxZ, float64(p.z))
	}

	st, _ := f.Stat()
	fmt.Printf("已写出 %s\n", out)
	fmt.Printf("  点数   %d\n", len(pts))
	fmt.Printf("  体积   %.2f MB\n", float64(st.Size())/(1<<20))
	fmt.Printf("  包围盒 [%.2f %.2f %.2f] .. [%.2f %.2f %.2f]\n",
		minX, minY, minZ, maxX, maxY, maxZ)
	fmt.Printf("  预期字节 %d\n", 8+len(pts)*15)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "生成失败:", err)
	os.Exit(1)
}
