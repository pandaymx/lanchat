//go:build !desktop

// Stub 占位：桌面端壳（Wails v3）需要 CGO 系统 WebView，默认构建
// （无 desktop tag）不含 CGO 代码（ADR-015）。带 -tags desktop 且目标
// 系统具备 WebView 依赖时，编译的是 main.go（真壳）。
//
// 默认构建给出可执行文件，避免 go build ./... 对空目录报错；真实桌面
// 二进制由 CI 原生 runner job 构建（AGENTS.md §6）。
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "lanchat desktop 需要 -tags desktop 构建（CGO，见 AGENTS.md ADR-015）；请使用 CI 桌面 job 产物。")
	os.Exit(1)
}
