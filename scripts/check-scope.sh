#!/usr/bin/env bash
# check-scope.sh — 按 AGENTS.md §2 的 Agent 责任田校验 PR 改动路径
#
# 规则：分支名（<type>/<milestone>-<简述>）中匹配到里程碑/scope 关键词后，
# 只允许改动该 Agent 负责的目录；公共文件（go.mod、CI、文档等）对 chore/ci 分支放行。
# 无法识别的分支只告警不拦截，避免误伤；release-please 分支直接放行。
#
# 输入环境变量：
#   HEAD_REF  PR 头部分支名
#   BASE_SHA  PR 基线提交 SHA
set -euo pipefail

branch="${HEAD_REF:-}"
base="${BASE_SHA:-origin/main}"

# release-please 机器人分支：改动由平台执行，放行。
if [[ "$branch" == release-please* ]]; then
  echo "release-please 分支，跳过 scope 校验"
  exit 0
fi

# 公共改动（A8 平台/发版 scope）
COMMON_PATHS='(^go\.(mod|sum)$)|((^|/)\.github/)|(^\.golangci\.(yml|yaml)$)|((^|/)Taskfile\.yml$)|(^configs/)|(^docs/)|(^scripts/)|(^CHANGELOG)|(^\.release-please-manifest)|(^release-please-config)'

case "$branch" in
  *m0*|*contract*|*appapi*)
    # A0 契约管家：协议 + 契约（契约冻结是一切起点）
    OWNED_PATHS='^internal/(protocol|appapi)/|^api/'
    ;;
  *m1*|*server*)
    # A1 后端核心
    OWNED_PATHS='^internal/(server|core|discover|relay)/|^cmd/lanchat/'
    ;;
  *m2*|*m3*|*transfer*)
    # A2 传输引擎
    OWNED_PATHS='^internal/transfer/'
    ;;
  *m4*|*win*)
    # A3 Windows 端
    OWNED_PATHS='^ui-win/'
    ;;
  *m5*|*linux*)
    # A4 Linux 端
    OWNED_PATHS='^ui-linux/'
    ;;
  *m6*|*android*)
    # A7 Android 端（含 gomobile 绑定）
    OWNED_PATHS='^ui-android/|^bindings/'
    ;;
  *m7*|*macos*)
    # A5 macOS 端
    OWNED_PATHS='^ui-macos/'
    ;;
  *m8*|*ios*)
    # A6 iOS 端
    OWNED_PATHS='^ui-ios/|^bindings/'
    ;;
  *m9*|*group*)
    # A2 群组（M9 复用传输引擎）
    OWNED_PATHS='^internal/group/'
    ;;
  *ci*|*chore*)
    # A8 平台/发版：公共文件全部放行
    OWNED_PATHS="$COMMON_PATHS"
    ;;
  *)
    echo "::warning::无法识别分支 $branch 的责任田，跳过 scope 校验（详见 AGENTS.md §2）"
    exit 0
    ;;
esac

mapfile -t changed < <(git diff --name-only "$base"...HEAD)

if [ "${#changed[@]}" -eq 0 ]; then
  echo "没有相对 $base 的文件改动"
  exit 0
fi

violations=()
for f in "${changed[@]}"; do
  if [[ "$f" =~ $OWNED_PATHS ]]; then
    continue
  fi
  # 契约分支以外，公共文件改动允许与任何 scope 共存（版本文件、CI 等）。
  if [[ "$branch" != *m0* && "$branch" != *contract* ]] \
     && [[ "$f" =~ $COMMON_PATHS ]]; then
    continue
  fi
  violations+=("$f")
done

if [ "${#violations[@]}" -gt 0 ]; then
  echo "::error::分支 $branch 越界改动了责任田之外的文件（AGENTS.md §2）："
  printf '  %s\n' "${violations[@]}"
  echo
  echo "允许路径：$OWNED_PATHS"
  exit 1
fi

echo "scope 校验通过：分支 $branch 的 ${#changed[@]} 个文件均在责任田内"
