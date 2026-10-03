#!/usr/bin/env bash
# perf-baseline.sh — 运行性能测试并与基线对比，超过容忍度则 FAIL。
#
# 用法：scripts/perf-baseline.sh [--update]
#   --update  将本次结果写回 configs/perf-baseline.json（不检查回退）
#
# 依赖：go, python3（解析 JSON 基线）
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASELINE="$REPO_ROOT/configs/perf-baseline.json"
UPDATE=false
[[ "${1:-}" == "--update" ]] && UPDATE=true

# ─── 运行性能测试 ─────────────────────────────────────────────────
echo ">>> 运行 TestPerf_1GiB_Throughput ..."
TEST_OUTPUT=$(cd "$REPO_ROOT" && go test -tags perf -count=1 -timeout 30m -v \
  -run TestPerf_1GiB_Throughput ./test/integration/... 2>&1) || {
  echo "$TEST_OUTPUT"
  echo "::error::性能测试运行失败"
  exit 1
}
echo "$TEST_OUTPUT"

# ─── 解析结果 ─────────────────────────────────────────────────────
# 测试输出格式：
#   1 GiB 耗时 X.XXs，吞吐 XXX.XX MiB/s（XXX.XX MB/s），堆增长 XX MiB
THROUGHPUT=$(echo "$TEST_OUTPUT" | grep -oP '吞吐 \K[0-9.]+' | head -1)
# 堆增长可能是负数（GC 回收），取绝对值
HEAP_RAW=$(echo "$TEST_OUTPUT" | grep -oP '堆增长 -?\K[0-9]+' | head -1)
HEAP_GROWTH=${HEAP_RAW:-0}

if [[ -z "$THROUGHPUT" ]]; then
  echo "::error::无法从测试输出解析吞吐量"
  exit 1
fi

echo ""
echo ">>> 结果：吞吐 ${THROUGHPUT} MiB/s，堆增长 ${HEAP_GROWTH} MiB"

# ─── 更新模式 ─────────────────────────────────────────────────────
if $UPDATE; then
  TODAY=$(date +%Y-%m-%d)
  python3 -c "
import json, sys
with open('$BASELINE') as f:
    b = json.load(f)
b['tests']['TestPerf_1GiB_Throughput']['throughput_mbps'] = $THROUGHPUT
b['tests']['TestPerf_1GiB_Throughput']['heap_growth_mib'] = $HEAP_GROWTH
b['updated'] = '$TODAY'
with open('$BASELINE', 'w') as f:
    json.dump(b, f, indent=2, ensure_ascii=False)
    f.write('\n')
"
  echo ">>> 基线已更新：$BASELINE"
  exit 0
fi

# ─── 对比基线 ─────────────────────────────────────────────────────
python3 -c "
import json, sys

with open('$BASELINE') as f:
    baseline = json.load(f)

t = baseline['tests']['TestPerf_1GiB_Throughput']
base_throughput = t['throughput_mbps']
base_heap = t['heap_growth_mib']
tolerance = t.get('tolerance_pct', 20)

actual_throughput = $THROUGHPUT
actual_heap = $HEAP_GROWTH

# 吞吐量：低于基线 (1 - tolerance/100) 则 FAIL
min_throughput = base_throughput * (1 - tolerance / 100)
# 堆增长：超过基线 (1 + tolerance/100) 则 FAIL
max_heap = base_heap * (1 + tolerance / 100)

fail = False

if actual_throughput < min_throughput:
    pct = (base_throughput - actual_throughput) / base_throughput * 100
    print(f'::error::吞吐回退 {pct:.1f}%（{actual_throughput:.1f} < {min_throughput:.1f} MiB/s，基线 {base_throughput} ±{tolerance}%）')
    fail = True
else:
    print(f'  吞吐 OK: {actual_throughput:.1f} MiB/s (基线 {base_throughput}, 下限 {min_throughput:.1f})')

if actual_heap > max_heap:
    pct = (actual_heap - base_heap) / max(1, base_heap) * 100
    print(f'::error::堆增长超标 {pct:.1f}%（{actual_heap} > {max_heap:.0f} MiB，基线 {base_heap} +{tolerance}%）')
    fail = True
else:
    print(f'  堆增长 OK: {actual_heap} MiB (基线 {base_heap}, 上限 {max_heap:.0f})')

sys.exit(1 if fail else 0)
"
