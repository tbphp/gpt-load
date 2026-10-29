#!/usr/bin/env bash
set -euo pipefail

# 两组互补、耗时接近：名称 A–O 的迁移契约一组，其余（包括未来新增测试）自动归入另一组。
# 按名称首字母切分，新增迁移契约会大致均匀地落到两组。
first='^TestExternal[A-O]\w*MigrationContract$'
run='^TestExternal'
skip='^$'
case "${1:-all}" in
  all) ;;
  first) run="${first}" ;;
  second) skip="${first}" ;;
  mysql | postgres)
    driver="$1"
    : "${GPT_LOAD_DATABASE_TEST_DSN:?primary database DSN is required}"
    : "${GPT_LOAD_DATABASE_SECONDARY_DSN:?secondary database DSN is required}"
    logs="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/${driver}-contract.XXXXXX")"
    cleanup() {
      for pid in $(jobs -pr); do
        kill "${pid}" 2>/dev/null || true
      done
      wait || true
      rm -rf "${logs}"
    }
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM

    bash "$0" first >"${logs}/first.log" 2>&1 &
    first_pid=$!
    GPT_LOAD_DATABASE_TEST_DSN="${GPT_LOAD_DATABASE_SECONDARY_DSN}" \
      bash "$0" second >"${logs}/second.log" 2>&1 &
    second_pid=$!
    result=0
    wait "${first_pid}" || result=1
    wait "${second_pid}" || result=1
    for shard in first second; do
      echo "::group::${driver} ${shard}"
      cat "${logs}/${shard}.log"
      echo '::endgroup::'
    done
    exit "${result}"
    ;;
  *) echo "unknown database test shard: $1" >&2; exit 2 ;;
esac

exec go test -v -count=1 ./internal/storage ./internal/control ./internal/requestlog ./internal/state/loader \
  -run "${run}" -skip "${skip}"
