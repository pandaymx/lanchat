// Command hub 是 LAN Chat 的服务端入口：管理客户端连接、路由消息、维护在线状态与多设备投递。
package main

import (
	"fmt"
	"os"
)

// 版本号由构建注入，见 Makefile LDFLAGS。不要在这里写死版本号。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("lanchat %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	fmt.Println("lanchat hub: 尚未实现")
}
