package i18n

import "embed"

// bundlesFS 嵌入 bundles/*.json 资源；通过 MustLoadEmbedded 暴露给 cmd/tui。
//
// 注意：embed.FS 的目录分隔符固定为 "/"，所以 bundles/<locale>.json
// 在跨平台（Windows / Linux）都按同一路径读。json.Unmarshal 与 io/fs
// 不关心 OS。
//
//go:embed bundles/*.json
var bundlesFS embed.FS

// MustLoadEmbedded 是 cmd/tui 启动期的便捷入口：直接从本包 embed.FS 加载
// 已嵌入的 bundles/*.json。内部走 MustLoad(bundlesFS, ...)。
//
// 与 Load/MustLoad 的关系：本函数只是把 bundlesFS 注入，省掉调用方再
// 持有 *embed.FS 的负担。
func MustLoadEmbedded(locales []string, fallback string) *Bundle {
	return MustLoad(bundlesFS, locales, fallback)
}
