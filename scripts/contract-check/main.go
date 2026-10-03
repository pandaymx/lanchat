// contract-check 校验五端 manifest 与源码实际引用的 API 方法是否一致。
//
// 工作原理：
//  1. 读取 api/ipc.schema.json 获取契约定义的全量方法列表。
//  2. 读取 api/contract/{platform}.methods.json 获取该端声明实现的方法清单。
//  3. 在对应 UI 源码树中 grep 每个方法的引用。
//  4. 报告两类偏差：
//     - phantom：manifest 声明了但源码未引用（实现缺失）
//     - untracked：源码引用了但 manifest 未声明（清单遗漏）
//
// 退出码：0 = 全部一致，1 = 有偏差或错误。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// schema 顶层结构（只取 requests 键）。
type schema struct {
	Requests map[string]json.RawMessage `json:"requests"`
}

// manifest 文件结构。
type manifest struct {
	Platform string   `json:"platform"`
	Methods  []string `json:"methods"`
}

// platform 定义每个 UI 端的元数据。
type platform struct {
	Name       string   // 显示名
	Dir        string   // UI 源码目录（相对仓库根）
	GrepFiles  []string // 文件后缀过滤
	Mode       string   // "jsonrpc" 或 "gomobile"
	RootSubDir string   // 额外缩限搜索范围（可选）
}

var platforms = map[string]platform{
	"win": {
		Name:      "Windows (C#/WinUI)",
		Dir:       "ui-win",
		GrepFiles: []string{".cs"},
		Mode:      "jsonrpc",
	},
	"linux": {
		Name:      "Linux (Rust/GTK)",
		Dir:       "ui-linux",
		GrepFiles: []string{".rs"},
		Mode:      "jsonrpc",
	},
	"macos": {
		Name:      "macOS (Swift/AppKit)",
		Dir:       "ui-macos",
		GrepFiles: []string{".swift"},
		Mode:      "jsonrpc",
	},
	"ios": {
		Name:      "iOS (Swift/gomobile)",
		Dir:       "ui-ios",
		GrepFiles: []string{".swift"},
		Mode:      "gomobile",
	},
	"android": {
		Name:      "Android (Kotlin/gomobile)",
		Dir:       "ui-android",
		GrepFiles: []string{".kt"},
		Mode:      "gomobile",
	},
}

func main() {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(2)
	}

	// 1. 读 schema。
	methods, err := loadSchemaMethods(filepath.Join(root, "api", "ipc.schema.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: 读取 schema: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("schema 定义 %d 个方法: %s\n", len(methods), strings.Join(methods, ", "))

	// 2. 逐端校验。
	totalIssues := 0
	for _, pKey := range sortedKeys(platforms) {
		p := platforms[pKey]
		fmt.Printf("\n=== %s (%s) ===\n", p.Name, pKey)

		manifestPath := filepath.Join(root, "api", "contract", pKey+".methods.json")
		mf, err := loadManifest(manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: 读取 manifest %s: %v\n", manifestPath, err)
			totalIssues++
			continue
		}

		srcDir := filepath.Join(root, p.Dir)
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "WARN: 目录 %s 不存在，跳过\n", srcDir)
			continue
		}

		// 收集源码中实际引用的方法。
		found := scanSource(srcDir, methods, p)

		manifestSet := toSet(mf.Methods)
		foundSet := toSet(found)

		// phantom: manifest 有，源码无。
		var phantoms []string
		for _, m := range mf.Methods {
			if !foundSet[m] {
				phantoms = append(phantoms, m)
			}
		}
		// untracked: 源码有，manifest 无。
		var untracked []string
		for _, f := range found {
			if !manifestSet[f] {
				untracked = append(untracked, f)
			}
		}

		if len(phantoms) == 0 && len(untracked) == 0 {
			fmt.Printf("  ✓ manifest (%d 方法) 与源码一致\n", len(mf.Methods))
		} else {
			for _, ph := range phantoms {
				fmt.Printf("  ✗ phantom:   %s（manifest 声明但源码未引用）\n", ph)
			}
			for _, ut := range untracked {
				fmt.Printf("  ✗ untracked: %s（源码引用但 manifest 未声明）\n", ut)
			}
			totalIssues += len(phantoms) + len(untracked)
		}
	}

	fmt.Println()
	if totalIssues > 0 {
		fmt.Printf("FAIL: 发现 %d 处偏差\n", totalIssues)
		os.Exit(1)
	}
	fmt.Println("OK: 五端契约校验通过")
}

// findRepoRoot 从当前目录向上查找含 go.mod 的目录。
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到 go.mod（仓库根）")
		}
		dir = parent
	}
}

// loadSchemaMethods 从 ipc.schema.json 提取 requests 键名（即方法名）。
func loadSchemaMethods(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	methods := make([]string, 0, len(s.Requests))
	for k := range s.Requests {
		methods = append(methods, k)
	}
	sort.Strings(methods)
	return methods, nil
}

// loadManifest 读取单端 manifest。
func loadManifest(path string) (*manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var mf manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

// scanSource 在源码目录中搜索方法引用，返回源码中实际引用的方法名列表（按 schema 名称）。
func scanSource(dir string, schemaMethods []string, p platform) []string {
	// 收集所有目标后缀的文件内容。
	var allContent strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无权限目录等
		}
		if info.IsDir() {
			// 跳过常见的非源码目录。
			base := filepath.Base(path)
			if base == "node_modules" || base == ".git" || base == "build" || base == "bin" || base == "obj" {
				return filepath.SkipDir
			}
			return nil
		}
		for _, suf := range p.GrepFiles {
			if strings.HasSuffix(path, suf) {
				data, err := os.ReadFile(path)
				if err == nil {
					allContent.Write(data)
					allContent.WriteByte('\n')
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: 遍历 %s: %v\n", dir, err)
	}

	content := allContent.String()
	var found []string
	for _, method := range schemaMethods {
		if matchMethod(content, method, p.Mode) {
			found = append(found, method)
		}
	}
	return found
}

// matchMethod 判断内容中是否引用了指定方法。
func matchMethod(content, method, mode string) bool {
	switch mode {
	case "jsonrpc":
		// 桌面端：方法名作为 JSON-RPC 字符串字面量出现，如 "GetState"。
		// 带引号匹配确保不会子串误匹配（"OfferFile" ≠ "OfferFileToGroup"）。
		return strings.Contains(content, `"`+method+`"`)
	case "gomobile":
		// 移动端：gomobile 将 Go 的 PascalCase 方法名转为 camelCase。
		camel := toCamelCase(method)
		if strings.Contains(content, camel) {
			return true
		}
		// Android 特殊处理：Go 的 GetXxx() 在 Kotlin 中变成属性 xxx（剥掉 get 前缀）。
		// 例如 GetStateJSON() → client!!.stateJSON（属性访问，无 get 前缀）。
		// 检测条件：方法以 "Get" 开头且第 4 个字符是大写字母。
		if strings.HasPrefix(method, "Get") && len(method) > 3 && unicode.IsUpper(rune(method[3])) {
			stripped := toCamelCase(method[3:])
			if strings.Contains(content, stripped) {
				return true
			}
		}
		return false
	}
	return false
}

// toCamelCase 将 PascalCase 转为 camelCase（首字母小写）。
func toCamelCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func sortedKeys(m map[string]platform) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
