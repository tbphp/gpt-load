#!/usr/bin/env bash
set -euo pipefail
umask 077

binary="${RELEASE_SMOKE_BINARY:?RELEASE_SMOKE_BINARY is required}"
checksum_file="${RELEASE_SMOKE_CHECKSUM_FILE:?RELEASE_SMOKE_CHECKSUM_FILE is required}"
release_version="${RELEASE_VERSION:?RELEASE_VERSION is required}"
port="${RELEASE_SMOKE_PORT:-39113}"
filename="$(basename "${binary}")"

path_mode() {
  local path="$1"
  if stat -c '%a' "${path}" >/dev/null 2>&1; then
    stat -c '%a' "${path}"
  else
    stat -f '%Lp' "${path}"
  fi
}

expected_hash="$(awk -v name="${filename}" '$2 == name {print $1}' "${checksum_file}")"
test -n "${expected_hash}"
if command -v sha256sum >/dev/null 2>&1; then
  before_hash="$(sha256sum "${binary}" | awk '{print $1}')"
else
  before_hash="$(shasum -a 256 "${binary}" | awk '{print $1}')"
fi
test "${before_hash}" = "${expected_hash}"

chmod +x "${binary}"
"${binary}" help >/dev/null

data_dir="$(mktemp -d)"
log_file="$(mktemp)"
pid=
cleanup() {
  if [[ -n "${pid}" ]]; then
    kill -TERM "${pid}" >/dev/null 2>&1 || true
    wait "${pid}" >/dev/null 2>&1 || true
  fi
  rm -rf "${data_dir}" "${log_file}"
}
trap cleanup EXIT

DATA_DIR="${data_dir}" \
  PORT="${port}" \
  "${binary}" >"${log_file}" 2>&1 &
pid=$!
for _ in $(seq 1 80); do
  if curl -fsS "http://127.0.0.1:${port}/health" >"${data_dir}/health.json"; then
    break
  fi
  sleep 0.25
done

test -s "${data_dir}/health.json"
test -f "${data_dir}/auth.key"
test -f "${data_dir}/encryption.key"
test -f "${data_dir}/gpt-load.db"
auth_key="$(cat "${data_dir}/auth.key")"
test -n "${auth_key}"
grep -F "\"version\":\"${release_version}\"" "${data_dir}/health.json"
curl -fsS "http://127.0.0.1:${port}/" | grep -F '<div id="app"></div>' >/dev/null
curl -fsS \
  -H "Authorization: Bearer ${auth_key}" \
  "http://127.0.0.1:${port}/api/usage?range=24h" >/dev/null
curl -fsS \
  -H "Authorization: Bearer ${auth_key}" \
  "http://127.0.0.1:${port}/api/model-prices" >/dev/null
curl -fsS \
  -X POST \
  -H "Authorization: Bearer ${auth_key}" \
  -H "Content-Type: application/json" \
  --data-binary '{"name":"Release Native Smoke Access"}' \
  "http://127.0.0.1:${port}/api/access-keys" >/dev/null

test "$(path_mode "${data_dir}")" = "700"
for asset in auth.key encryption.key gpt-load.db gpt-load.db-wal gpt-load.db-shm; do
  test -f "${data_dir}/${asset}"
  test "$(path_mode "${data_dir}/${asset}")" = "600"
done

kill -TERM "${pid}"
wait "${pid}"
pid=

if command -v sha256sum >/dev/null 2>&1; then
  after_hash="$(sha256sum "${binary}" | awk '{print $1}')"
else
  after_hash="$(shasum -a 256 "${binary}" | awk '{print $1}')"
fi
test "${after_hash}" = "${expected_hash}"
