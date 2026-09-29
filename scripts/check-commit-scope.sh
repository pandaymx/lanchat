#!/usr/bin/env bash
# check-commit-scope.sh — 校验提交信息中的 scope 是否属于 AGENTS.md §5 写死的枚举
#
# PR：校验 BASE_SHA..HEAD 范围；push 到 main：校验最近一个提交。
set -euo pipefail

# scope 枚举（与 AGENTS.md §5 保持一致，禁止自造）
ALLOWED_SCOPES='protocol|server|core|transfer|group|relay|discover|appapi|bindings|ipc|ui-win|ui-linux|ui-macos|ui-ios|ui-android|config|ci|docs'

base="${BASE_SHA:-}"
if [ -n "$base" ]; then
  range="${base}..HEAD"
else
  range="HEAD~1..HEAD"
fi

mapfile -t subjects < <(git log --format=%s "$range")

if [ "${#subjects[@]}" -eq 0 ]; then
  echo "范围内没有提交，跳过"
  exit 0
fi

# Conventional Commit：type(scope): subject 或 type: subject（无 scope 合法）
cc_re='^[a-z]+(\(([^)]+)\))?!?: '

bad=()
for s in "${subjects[@]}"; do
  # revert 提交形如：revert: feat(scope): ...；先剥掉 revert: 前缀。
  s="${s#revert: }"
  if [[ "$s" =~ $cc_re ]]; then
    scope="${BASH_REMATCH[2]}"
    if [ -n "$scope" ] && ! [[ "$scope" =~ ^($ALLOWED_SCOPES)$ ]]; then
      bad+=("$s")
    fi
  fi
done

if [ "${#bad[@]}" -gt 0 ]; then
  echo "::error::以下提交使用了 AGENTS.md §5 枚举之外的 scope："
  printf '  %s\n' "${bad[@]}"
  echo
  echo "允许的 scope：${ALLOWED_SCOPES//|/, }"
  exit 1
fi

echo "commit scope 校验通过（${#subjects[@]} 个提交）"
