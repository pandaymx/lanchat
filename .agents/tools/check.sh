#!/usr/bin/env bash
# lanchat 仓库一致性检查（多 AI Agent 协作用）
#
# 用法：仓库根目录执行  bash .agents/tools/check.sh
# 覆盖 4 类历史踩坑，防止任何 agent 会话重新踩：
#   1. templ 版本 pin 不一致（go.mod 与 CI 漂移 → 生成代码与运行时冲突）
#   2. gci 命令缺 sections 参数（会把全仓无关文件的 import 分组重排）
#   3. commitlint scope 白名单与 AGENTS.md 声明不一致
#   4. i18n bundle key 集合不一致 / 代码引用了不存在的 key
#
# 零依赖：仅用标准库 grep/sed/awk/comm，WSL Arch Linux 直接可跑。
# 新增检查项时保持「输出值 + 判定」结构，方便多 agent 快速定位。

set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || { echo "❌ 无法进入仓库根目录 $ROOT"; exit 1; }

FAIL=0
PASS=0
ok()  { PASS=$((PASS + 1)); echo "  ✅ $1"; }
bad() { FAIL=$((FAIL + 1)); echo "  ❌ $1"; }

# ---------- 1. templ 版本 pin 一致性 ----------
echo "== 1. templ 版本 pin 一致性 =="
go_mod_ver="$(sed -n 's/.*github.com\/a-h\/templ v\([0-9][0-9.]*\).*/\1/p' go.mod | head -1)"
ci_vers="$(sed -n 's/.*templ\/cmd\/templ@v\([0-9][0-9.]*\).*/\1/p' .github/workflows/ci.yml | sort -u)"
if [ -n "$go_mod_ver" ] && [ "$(printf '%s\n' "$ci_vers" | wc -l)" -eq 1 ] && [ "$go_mod_ver" = "$ci_vers" ]; then
  ok "templ go.mod($go_mod_ver) == CI 全部 pin($ci_vers)"
else
  bad "templ pin 不一致：go.mod=$go_mod_ver，CI pin=[$(echo "$ci_vers" | tr '\n' ' ')]（AGENTS.md：三处必须一致）"
fi

# ---------- 2. gci sections 参数 ----------
echo "== 2. gci sections 参数 =="
# Makefile fmt 的 gci 必须与 lefthook/.golangci.yml 同参数，否则全仓 import 分组被重排
makefile_gci="$(sed -n 's/.*\(gci write.*\)/\1/p' Makefile)"
if printf '%s' "$makefile_gci" | grep -q -- "--no-lex-order" && printf '%s' "$makefile_gci" | grep -q "prefix(github.com/pandaymx/lanchat)"; then
  ok "Makefile gci 带 sections 参数"
else
  bad "Makefile gci 缺 --no-lex-order / -s / prefix 参数（当前: $makefile_gci）→ 会把全仓 import 分组重排"
fi

lefthook_gci="$(sed -n 's/.*\(gci write.*\)/\1/p' lefthook.yml)"
if printf '%s' "$lefthook_gci" | grep -q "prefix(github.com/pandaymx/lanchat)"; then
  ok "lefthook.yml gci 带 sections 参数"
else
  bad "lefthook.yml gci 缺 sections 参数（当前: $lefthook_gci）"
fi

golangci_gci="$(sed -n '/gci:/,/issues:/p' .golangci.yml)"
if printf '%s' "$golangci_gci" | grep -q "prefix(github.com/pandaymx/lanchat)"; then
  ok ".golangci.yml gci sections 含 prefix"
else
  bad ".golangci.yml gci sections 缺 prefix(github.com/pandaymx/lanchat)"
fi

# ---------- 3. commitlint scope 白名单 ----------
echo "== 3. commitlint scope 白名单 =="
agents_scopes="$(grep 'scope 只能是' AGENTS.md | grep -o '`[a-z][a-z]*`' | tr -d '`' | sort)"
# 只取 8 空格缩进的数组元素行，排除 severity 参数行（'always'）
config_scopes="$(sed -n "/'scope-enum'/,/],/p" commitlint.config.js | grep -E "^        '[a-z][a-z]*'," | grep -o "'[a-z][a-z]*'" | tr -d "'" | sort)"
if [ "$agents_scopes" = "$config_scopes" ]; then
  ok "AGENTS.md 与 commitlint.config.js scope 一致（$(echo "$config_scopes" | tr '\n' ' ')）"
else
  bad "scope 白名单不一致：\n    仅 AGENTS.md: $(comm -23 <(echo "$agents_scopes") <(echo "$config_scopes") | tr '\n' ' ')\n    仅 commitlint: $(comm -13 <(echo "$agents_scopes") <(echo "$config_scopes") | tr '\n' ' ')"
fi

# ---------- 4. i18n key 完整性 ----------
echo "== 4. i18n key 完整性 =="
en_keys="$(sed -n 's/^  "\([^"]*\)":.*/\1/p' internal/i18n/bundles/en.json | sort)"
zh_keys="$(sed -n 's/^  "\([^"]*\)":.*/\1/p' internal/i18n/bundles/zh-cn.json | sort)"
if [ "$en_keys" = "$zh_keys" ]; then
  ok "en.json 与 zh-cn.json key 集合一致（$(echo "$en_keys" | wc -l) 个 key）"
else
  bad "en/zh-cn key 集合不一致：\n    仅 en:    $(comm -23 <(echo "$en_keys") <(echo "$zh_keys") | tr '\n' ' ')\n    仅 zh-cn: $(comm -13 <(echo "$en_keys") <(echo "$zh_keys") | tr '\n' ' ')"
fi

# 排除注释行（i18n_test.go 注释里出现过 m.t("online") 这类假引用）
used_keys="$(grep -rnE '\.t\("[a-z.]+"\)' pkg/tui | grep -vE ':[0-9]+:[[:space:]]*//' | grep -oE '"[a-z.]+"' | tr -d '"' | sort -u)"
# web 端（M4.7）：模板里 T(tr, "web.xxx") 与 handler 里 templates.T(...) 引用，
# 扫 internal/webui 下 .go / .templ（*_templ.go 生成物不入库，扫了也无害）。
web_used_keys="$(grep -rhoE '"web\.[a-z._]+"' internal/webui --include='*.go' --include='*.templ' | tr -d '"' | sort -u)"
used_keys="$(printf '%s\n%s\n' "$used_keys" "$web_used_keys" | sort -u)"
missing="$(comm -23 <(printf '%s\n' "$used_keys") <(printf '%s\n' "$en_keys"))"
if [ -z "$missing" ]; then
  ok "pkg/tui + internal/webui 引用的 key 全部存在于 bundle（$(echo "$used_keys" | wc -l) 个引用 key）"
else
  bad "代码引用了 bundle 不存在的 key: $(echo "$missing" | tr '\n' ' ')"
fi

# ---------- 汇总 ----------
echo "----------------------------------------"
echo "结果：$PASS 通过 / $FAIL 失败"
[ "$FAIL" -eq 0 ] || echo "❌ 请修复后再提交"
exit $FAIL
