package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

// staticFS 嵌入 static/ 下的 htmx、SSE 扩展与样式表。
//
// 为什么嵌入而不是用 http.FileServer 读磁盘：
//  1. lanchat 是局域网自托管小工具，`bin/web` 单文件拷到任意机器即可跑，
//     不需要附带一个 static 目录（也避免"改了 CSS 没同步"的部署事故）
//  2. 与 internal/i18n 的 embed.FS 做法一致（项目内已有先例）
//
// 注意：`//go:embed all:static` 的 all: 前缀才能带上点开头的文件；
// 当前 static/ 下只有 .js / .css，但仍用 all: 以防将来加 .map / .svg。
//
//go:embed all:static
var staticFS embed.FS

// StaticHandler 返回 /assets/ 前缀的静态文件服务。
//
// 用法：mux.Handle("/assets/", http.StripPrefix("/assets/", StaticHandler()))
//
// 缓存策略：局域网 MVP 阶段文件名不带内容哈希，所以用 no-cache 而不是
// immutable——否则改了 style.css 后浏览器会一直用旧的（M4 迭代期高频）。
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// embed.FS 的目录在编译期就确定，fs.Sub 不可能失败。
		// 这里 panic 而不是静默降级：带上一个坏掉的静态服务比启动失败更难查。
		panic("webui: embed static sub-tree failed: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.FileServer(http.FS(sub)).ServeHTTP(w, r)
	})
}
