// Command androidhub 是 Android 内嵌 hub 的 c-shared 构建入口。
//
// 去中心化第 4 步：把 pkg/hubserver 编进安卓——Dart 经 FFI 调本 so，
// App 启动时进程内起回环嵌入式 hub（127.0.0.1 随机端口 + mesh + mDNS），
// 安卓变成局域网里平等的一个 mesh 节点，不再依赖外部 hub 进程。
//
// 编译（arm64-v8a；NDK r30+，Linux 侧 clang）：
//
//	CC=$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang \
//	GOOS=android GOARCH=arm64 CGO_ENABLED=1 \
//	go build -buildmode=c-shared -o liblanchub.so ./mobile/cmd/androidhub
//
// 导出符号：lanchub_start / lanchub_stop / lanchub_addr / lanchub_free。
// 返回的 char* 由 C 内存持有，调用方必须 lanchub_free 释放（防止泄漏）。
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"unsafe"

	"github.com/pandaymx/lanchat/mobile"
)

// lanchub_start 起嵌入式 hub，返回 ws 地址的 C 字符串；失败返回 nil。
// 调用方负责 lanchub_free。幂等：已启动返回现有地址。
//
//export lanchub_start
func lanchub_start(dataDir, nodeID, version *C.char) *C.char {
	ws, err := mobile.Start(C.GoString(dataDir), C.GoString(nodeID), C.GoString(version))
	if err != nil {
		return nil
	}
	return C.CString(ws)
}

// lanchub_stop 停掉嵌入式 hub（幂等）。
//
//export lanchub_stop
func lanchub_stop() {
	mobile.Stop()
}

// lanchub_addr 返回当前 ws 地址（未启动返回 nil）。调用方负责 lanchub_free。
//
//export lanchub_addr
func lanchub_addr() *C.char {
	addr := mobile.Addr()
	if addr == "" {
		return nil
	}
	return C.CString(addr)
}

// lanchub_free 释放由本包 C.CString 分配的内存。
//
//export lanchub_free
func lanchub_free(p *C.char) {
	C.free(unsafe.Pointer(p))
}

func main() {}
