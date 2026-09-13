#!/usr/bin/env bash
set -euo pipefail

# 两组互补：增量迁移契约单独运行，其余（包括未来新增测试）自动归入另一组。
migrations='^TestExternal(AccessKeyPrefix|CustomAccessKey|PriceMultiplier|ModelCooldown|ValidationProtocol)MigrationContract$'
run='^TestExternal'
skip='^$'
case "${1:-all}" in
  all | postgres) ;;
  mysql-migrations) run="${migrations}" ;;
  mysql-rest) skip="${migrations}" ;;
  mysql)
    : "${GPT_LOAD_DATABASE_TEST_DSN:?primary MySQL DSN is required}"
    : "${GPT_LOAD_DATABASE_SECONDARY_DSN:?secondary MySQL DSN is required}"
    logs="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/mysql-contract.XXXXXX")"
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

    bash "$0" mysql-migrations >"${logs}/migrations.log" 2>&1 &
    migrations_pid=$!
    GPT_LOAD_DATABASE_TEST_DSN="${GPT_LOAD_DATABASE_SECONDARY_DSN}" \
      bash "$0" mysql-rest >"${logs}/rest.log" 2>&1 &
    rest_pid=$!
    result=0
    wait "${migrations_pid}" || result=1
    wait "${rest_pid}" || result=1
    for shard in migrations rest; do
      echo "::group::MySQL ${shard}"
      cat "${logs}/${shard}.log"
      echo '::endgroup::'
    done
    exit "${result}"
    ;;
  *) echo "unknown database test shard: $1" >&2; exit 2 ;;
esac

exec go test -v -count=1 ./internal/storage ./internal/control ./internal/requestlog \
  -run "${run}" -skip "${skip}"
