// Command illust3d 是本项目的唯一入口：插画 → 点云 → 风格化渲染。
//
// 当前仅为项目骨架，业务逻辑尚未实现。阶段计划见 docs/scope.md。
// 选型与接口规范见 docs/selection.md，Go 职责边界见 docs/go-scope.md。
package main

import (
	"flag"
	"fmt"
	"os"
)

// version 由 -ldflags 注入，例如
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/illust3d
var version = "0.0.0-dev"

func main() {
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("illust3d %s\n", version)
		return
	}

	fmt.Fprintln(os.Stderr, "illust3d: 骨架已就位，功能尚未实现。")
	fmt.Fprintln(os.Stderr, "  阶段计划   docs/scope.md")
	fmt.Fprintln(os.Stderr, "  选型与规范 docs/selection.md")
	fmt.Fprintln(os.Stderr, "  Go 职责    docs/go-scope.md")
}
