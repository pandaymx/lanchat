// Package appdir 解析跨平台的默认数据/下载目录。
//
// 背景：hub / tui 曾把 lanchat.db 与 lanchat-files/ 放在「当前工作目录」，
// Windows 安装器把程序装进 C:\Program Files\LAN Chat Hub（普通用户只读）
// 后，双击运行会在安装目录里写库和文件而失败。本包按各平台规范返回
// 可写目录，安装位置不再影响运行。
package appdir

import (
	"os"
	"path/filepath"
	"runtime"
)

// AppName 返回平台惯用的应用目录名：Windows 用安装器展示名
// 「LAN Chat Hub」，其余平台用小写 lanchat（XDG / Library 惯例）。
func AppName() string {
	if runtime.GOOS == "windows" {
		return "LAN Chat Hub"
	}
	return "lanchat"
}

// DataDir 返回应用的可写数据目录（hub 的 db 与 blob 存储放这里）：
//
//	Windows: %LOCALAPPDATA%\<App>
//	Linux:   $XDG_DATA_HOME/<app>（缺省 ~/.local/share/<app>）
//	macOS:   ~/Library/Application Support/<App>
//
// 任何基准目录都取不到时回退当前工作目录（与历史行为一致，可诊断）。
func DataDir(app string) string {
	switch runtime.GOOS {
	case "windows":
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, app)
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, "Library", "Application Support", app)
		}
	default: // linux / freebsd / netbsd / ...
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, app)
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, ".local", "share", app)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, app)
	}
	return app
}

// DownloadDir 返回用户下载目录下的应用子目录（tui 保存收到的附件）：
//
//	Windows: %USERPROFILE%\Downloads\<App>
//	Linux/macOS: ~/Downloads/<app>
//
// 环境变量 LANCHAT_DOWNLOAD_DIR 可覆盖（供测试与自定义）。
// 取不到下载目录时回退到 DataDir/downloads。
func DownloadDir(app string) string {
	if d := os.Getenv("LANCHAT_DOWNLOAD_DIR"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "windows":
		if home := os.Getenv("USERPROFILE"); home != "" {
			return filepath.Join(home, "Downloads", app)
		}
	default:
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, "Downloads", app)
		}
	}
	return filepath.Join(DataDir(app), "downloads")
}
