#!/usr/bin/env bash
set -euo pipefail
umask 077

release_version="${RELEASE_VERSION:-v2.0.0-local}"
suffix="${RELEASE_SMOKE_SUFFIX:-local-$$}"
source_image="${RELEASE_SMOKE_SOURCE_IMAGE:-}"
trivy_image="${RELEASE_SMOKE_TRIVY_IMAGE:-aquasec/trivy:0.72.0@sha256:cffe3f5161a47a6823fbd23d985795b3ed72a4c806da4c4df16266c02accdd6f}"
case "${suffix}" in
  *[!A-Za-z0-9_-]*)
    printf 'RELEASE_SMOKE_SUFFIX contains unsupported characters\n' >&2
    exit 1
    ;;
esac

image="gpt-load-release-smoke:${suffix}"
container="gpt-load-release-smoke-${suffix}"
probe="gpt-load-release-probe-${suffix}"
volume="gpt-load-release-smoke-${suffix}"
app_port="${RELEASE_SMOKE_APP_PORT:-39413}"
fake_port="${RELEASE_SMOKE_FAKE_PORT:-39414}"
base_url="http://127.0.0.1:${app_port}"
task_tmp="$(mktemp -d)"
fake_pid=

exec 3>&1 4>&2
exec >"${task_tmp}/smoke.stdout" 2>"${task_tmp}/smoke.stderr"

cleanup_temp() {
  local exit_code=$?
  rm -rf "${task_tmp}"
  if ((exit_code != 0)); then
    printf 'release Docker smoke failed; captured output withheld to protect credentials\n' >&4
  fi
}
trap cleanup_temp EXIT

for target in "${container}" "${probe}"; do
  if docker container inspect "${target}" >/dev/null 2>&1; then
    printf 'task container already exists: %s\n' "${target}" >&4
    exit 1
  fi
done
if docker image inspect "${image}" >/dev/null 2>&1 ||
  docker volume inspect "${volume}" >/dev/null 2>&1; then
  printf 'task image or volume already exists\n' >&4
  exit 1
fi

cleanup() {
  local exit_code=$?
  if [[ -n "${fake_pid}" ]]; then
    kill "${fake_pid}" >/dev/null 2>&1 || true
    wait "${fake_pid}" >/dev/null 2>&1 || true
  fi
  docker rm -f "${container}" "${probe}" >/dev/null 2>&1 || true
  docker volume rm "${volume}" >/dev/null 2>&1 || true
  docker image rm "${image}" >/dev/null 2>&1 || true
  rm -rf "${task_tmp}"
  if ((exit_code != 0)); then
    printf 'release Docker smoke failed; captured output withheld to protect credentials\n' >&4
  fi
}
trap cleanup EXIT

cat >"${task_tmp}/fake_upstream.py" <<'PY'
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        return

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length:
            self.rfile.read(length)
        body = {
            "id": "task13-complete-usage",
            "object": "chat.completion",
            "created": 1785100000,
            "model": "task13-release-model",
            "choices": [{
                "index": 0,
                "message": {"role": "assistant", "content": "ok"},
                "finish_reason": "stop",
            }],
            "usage": {
                "prompt_tokens": 7,
                "completion_tokens": 5,
                "total_tokens": 12,
            },
        }
        encoded = json.dumps(body, separators=(",", ":")).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

ThreadingHTTPServer(("0.0.0.0", int(os.environ["FAKE_PORT"])), Handler).serve_forever()
PY
FAKE_PORT="${fake_port}" python3 "${task_tmp}/fake_upstream.py" \
  >"${task_tmp}/fake.stdout" 2>"${task_tmp}/fake.stderr" &
fake_pid=$!

docker_arch="$(docker info --format '{{.Architecture}}')"
case "${docker_arch}" in
  arm64 | aarch64)
    platform="linux/arm64"
    ;;
  amd64 | x86_64)
    platform="linux/amd64"
    ;;
  *)
    printf 'unsupported Docker architecture: %s\n' "${docker_arch}" >&4
    exit 1
    ;;
esac

if [[ -n "${source_image}" ]]; then
  test "${source_image}" != "${image}"
  docker pull --platform "${platform}" "${source_image}"
  docker image tag "${source_image}" "${image}"
else
  docker build \
    --platform "${platform}" \
    --build-arg "VERSION=${release_version}" \
    -t "${image}" .
fi
docker run --rm \
  --volume /var/run/docker.sock:/var/run/docker.sock \
  "${trivy_image}" image \
  --scanners vuln \
  --severity CRITICAL,HIGH \
  --ignore-unfixed \
  --exit-code 1 \
  --no-progress \
  "${image}"
test "$(docker image inspect -f '{{.Config.User}}' "${image}")" = "10001:10001"
docker image inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${image}" |
  grep -Fx 'HOST=0.0.0.0' >/dev/null

docker volume create "${volume}" >/dev/null
docker run --name "${probe}" \
  --volume "${volume}:/app/data" \
  --entrypoint /bin/sh \
  "${image}" -ceu '
    test "$(id -u):$(id -g)" = "10001:10001"
    test -r /app/licenses/LICENSE
    test -r /app/licenses/THIRD_PARTY_NOTICES.md
    test -r /app/licenses/Apache-2.0.txt
    test -r /app/licenses/MIT.txt
    test -r /app/licenses/MPL-2.0.txt
    printf canary >/app/data/release-write-canary
    test "$(cat /app/data/release-write-canary)" = canary
    rm /app/data/release-write-canary
  '
docker rm "${probe}" >/dev/null

start_container() {
  docker run -d \
    --name "${container}" \
    --add-host host.docker.internal:host-gateway \
    --publish "${app_port}:3001" \
    --volume "${volume}:/app/data" \
    "${image}" >/dev/null
}

wait_for_health() {
  for _ in $(seq 1 120); do
    if curl -fsS "${base_url}/health" >"${task_tmp}/health.json"; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

api_get() {
  local path="$1"
  curl -fsS \
    -H "Authorization: Bearer ${auth_key}" \
    "${base_url}${path}"
}

api_write() {
  local method="$1"
  local path="$2"
  local body="$3"
  local idempotency_key="${4:-}"
  local idempotency_header=()
  if [[ -n "${idempotency_key}" ]]; then
    idempotency_header=(-H "Idempotency-Key: ${idempotency_key}")
  fi
  curl -fsS \
    -X "${method}" \
    -H "Authorization: Bearer ${auth_key}" \
    -H "Content-Type: application/json" \
    "${idempotency_header[@]}" \
    --data-binary "${body}" \
    "${base_url}${path}"
}

start_container
wait_for_health
first_container_id="$(docker inspect -f '{{.Id}}' "${container}")"
test "$(docker exec "${container}" id -u):$(docker exec "${container}" id -g)" = "10001:10001"
test "$(docker exec "${container}" printenv DATA_DIR)" = "/app/data"
test "$(docker exec "${container}" printenv HOST)" = "0.0.0.0"

for asset in auth.key encryption.key gpt-load.db; do
  docker exec "${container}" test -f "/app/data/${asset}"
done
auth_key="$(docker exec "${container}" cat /app/data/auth.key)"
encryption_key="$(docker exec "${container}" cat /app/data/encryption.key)"
test -n "${auth_key}"
test -n "${encryption_key}"
auth_hash_before="$(docker exec "${container}" sha256sum /app/data/auth.key | awk '{print $1}')"
encryption_hash_before="$(
  docker exec "${container}" sha256sum /app/data/encryption.key | awk '{print $1}'
)"

node -e '
  const fs=require("fs");
  const value=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
  if(value.version!==process.argv[2]) process.exit(1);
' "${task_tmp}/health.json" "${release_version}"
curl -fsS "${base_url}/" | grep -F '<div id="app"></div>' >/dev/null
test "$(curl -sS -o /dev/null -w '%{http_code}' "${base_url}/api/usage")" = "401"
test "$(
  curl -sS -o /dev/null -w '%{http_code}' \
    -H 'Content-Type: application/json' \
    --data-binary '{"model":"task13-release-model","messages":[]}' \
    "${base_url}/v1/chat/completions"
)" = "401"
api_get "/api/usage?range=24h" >"${task_tmp}/usage-empty.json"
api_get "/api/model-prices" >"${task_tmp}/prices-empty.json"

credential_secret="task13-credential-${suffix}-$(openssl rand -hex 12)"
group_idempotency_key="$(python3 -c 'import uuid; print(uuid.uuid4())')"
group_response="$(
  api_write POST "/api/groups" "$(
    node -e '
      process.stdout.write(JSON.stringify({
        name:"Task13 Release Smoke Group",
        channel_id:"openai_compatible",
        params:{base_url:process.argv[1]},
        models:[{id:"task13-release-model",alias:"",alias_enabled:false}],
        credentials:process.argv[2],
        confirm_same_target:false,
      }));
    ' "http://host.docker.internal:${fake_port}/v1" "${credential_secret}"
  )" "${group_idempotency_key}"
)"
group_id="$(
  printf '%s' "${group_response}" | node -e '
    const fs=require("fs");
    const value=JSON.parse(fs.readFileSync(0,"utf8"));
    const group=value.data;
    if(value.code!==0||!group||group.group_name!=="Task13 Release Smoke Group"||
       !Number.isSafeInteger(group.group_id)||group.group_id<=0||
       group.credentials_added!==1||group.credentials_duplicated!==0) process.exit(1);
    process.stdout.write(String(group.group_id));
  '
)"
unset group_response
test -n "${group_id}"

model_price_list_path="/api/model-prices?usage=in_use&status=all&page=1&page_size=100"
model_price_list="$(api_get "${model_price_list_path}")"
model_price_id="$(
  printf '%s' "${model_price_list}" | node -e '
    const fs=require("fs");
    const value=JSON.parse(fs.readFileSync(0,"utf8"));
    const channelID=process.argv[1];
    const modelID=process.argv[2];
    const items=value.data&&value.data.items;
    if(value.code!==0||!Array.isArray(items)) process.exit(1);
    const matches=items.filter(item=>
      item&&item.channel_id===channelID&&item.model_id===modelID
    );
    if(matches.length!==1||!Number.isSafeInteger(matches[0].id)||matches[0].id<=0) {
      process.exit(1);
    }
    process.stdout.write(String(matches[0].id));
  ' "openai_compatible" "task13-release-model"
)"
unset model_price_list
test -n "${model_price_id}"

api_write PUT "/api/model-prices/${model_price_id}" '{
  "input":"1",
  "output":"2",
  "cache_read":"3",
  "cache_write":"4",
  "confirm_unpriced":false
}' >"${task_tmp}/price-update.json"

access_key_idempotency_key="$(python3 -c 'import uuid; print(uuid.uuid4())')"
access_response="$(
  api_write POST "/api/access-keys" '{"name":"Task13 Release Smoke Access"}' \
    "${access_key_idempotency_key}"
)"
access_key="$(
  printf '%s' "${access_response}" | node -e '
    const fs=require("fs");
    const value=JSON.parse(fs.readFileSync(0,"utf8"));
    if(value.code!==0||!value.data.key.startsWith("sk-gl-")) process.exit(1);
    process.stdout.write(value.data.key);
  '
)"
unset access_response
test -n "${access_key}"

curl -fsS \
  -H "Authorization: Bearer ${access_key}" \
  -H "Content-Type: application/json" \
  --data-binary '{
    "model":"task13-release-model",
    "messages":[{"role":"user","content":"usage canary"}]
  }' \
  "${base_url}/v1/chat/completions" >"${task_tmp}/data-response.json"
node -e '
  const fs=require("fs");
  const value=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
  if(value.choices[0].finish_reason!=="stop") process.exit(1);
  if(value.usage.prompt_tokens!==7||value.usage.completion_tokens!==5) process.exit(1);
' "${task_tmp}/data-response.json"

usage_complete=false
for _ in $(seq 1 80); do
  api_get "/api/usage?range=24h" >"${task_tmp}/usage-first.json"
  if node -e '
    const fs=require("fs");
    const value=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
    const summary=value.data&&value.data.summary;
    if(!summary||summary.request_count<1) process.exit(1);
    if(summary.uncached_input_tokens!==7||summary.output_tokens!==5||
       summary.total_tokens!==12||summary.usage_missing_count!==0||
       summary.partial_count!==0||summary.unpriced_request_count!==0) process.exit(1);
  ' "${task_tmp}/usage-first.json"; then
    usage_complete=true
    break
  fi
  sleep 0.25
done
test "${usage_complete}" = true
test "$(docker exec "${container}" stat -c '%a' /app/data)" = "700"
for asset in auth.key encryption.key gpt-load.db gpt-load.db-wal gpt-load.db-shm; do
  test "$(docker exec "${container}" stat -c '%a' "/app/data/${asset}")" = "600"
done

docker stop --time 15 "${container}" >/dev/null
docker logs "${container}" >"${task_tmp}/container-first.log" 2>&1
test "$(docker inspect -f '{{.State.ExitCode}}' "${container}")" = "0"
test "$(docker inspect -f '{{.State.OOMKilled}}' "${container}")" = "false"
docker rm "${container}" >/dev/null

start_container
wait_for_health
second_container_id="$(docker inspect -f '{{.Id}}' "${container}")"
test "${first_container_id}" != "${second_container_id}"
restored_auth_key="$(docker exec "${container}" cat /app/data/auth.key)"
restored_encryption_key="$(docker exec "${container}" cat /app/data/encryption.key)"
test "${restored_auth_key}" = "${auth_key}"
test "${restored_encryption_key}" = "${encryption_key}"
test "$(
  docker exec "${container}" sha256sum /app/data/auth.key | awk '{print $1}'
)" = "${auth_hash_before}"
test "$(
  docker exec "${container}" sha256sum /app/data/encryption.key | awk '{print $1}'
)" = "${encryption_hash_before}"

api_get "/api/groups" >"${task_tmp}/groups-second.json"
api_get "${model_price_list_path}" >"${task_tmp}/prices-second.json"
api_get "/api/usage?range=24h" >"${task_tmp}/usage-second.json"
access_list="$(api_get "/api/access-keys")"
printf '%s' "${access_list}" | node -e '
  const fs=require("fs");
  const value=JSON.parse(fs.readFileSync(0,"utf8"));
  if(!value.data.items.some(item=>item.name==="Task13 Release Smoke Access")) process.exit(1);
'
unset access_list
node -e '
  const fs=require("fs");
  const groups=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));
  const prices=JSON.parse(fs.readFileSync(process.argv[2],"utf8"));
  const usage=JSON.parse(fs.readFileSync(process.argv[3],"utf8"));
  const groupID=Number(process.argv[4]);
  const priceID=Number(process.argv[5]);
  const modelID=process.argv[6];
  const groupItems=groups.data&&groups.data.items;
  if(!Array.isArray(groupItems)||!groupItems.some(item=>
    item.id===groupID&&item.name==="Task13 Release Smoke Group"&&
    item.channel_id==="openai_compatible"&&item.credential_counts.total===1
  )) process.exit(1);
  const items=prices.data&&prices.data.items;
  if(!Array.isArray(items)||!Number.isSafeInteger(priceID)||priceID<=0) process.exit(1);
  const matches=items.filter(item=>
    item&&item.channel_id==="openai_compatible"&&item.model_id===modelID
  );
  if(matches.length!==1||matches[0].id!==priceID||
     matches[0].method!=="user_set"||matches[0].pricing_status!=="configured") {
    process.exit(1);
  }
  const persisted=matches[0].prices;
  if(!persisted||persisted.input!=="1"||persisted.output!=="2"||
     persisted.cache_read!=="3"||persisted.cache_write!=="4") process.exit(1);
  if(usage.data.summary.request_count<1||usage.data.summary.total_tokens!==12) process.exit(1);
' \
  "${task_tmp}/groups-second.json" \
  "${task_tmp}/prices-second.json" \
  "${task_tmp}/usage-second.json" \
  "${group_id}" \
  "${model_price_id}" \
  "task13-release-model"
curl -fsS \
  -H "Authorization: Bearer ${access_key}" \
  "${base_url}/v1/models" >"${task_tmp}/models-second.json"

docker stop --time 15 "${container}" >/dev/null
docker logs "${container}" >"${task_tmp}/container-second.log" 2>&1
test "$(docker inspect -f '{{.State.ExitCode}}' "${container}")" = "0"

summary_file="${task_tmp}/summary.txt"
{
  printf 'platform=%s\n' "${platform}"
  printf 'configured_user=10001:10001\n'
  printf 'image_and_container_host=0.0.0.0\n'
  printf 'direct_docker_run_publish_reachable=true\n'
  printf 'managed_data_dir_mode=0700\n'
  printf 'managed_recovery_set_mode=0600\n'
  printf 'write_delete_canary=true\n'
  printf 'generated_assets=true\n'
  printf 'unauthenticated_management_and_data_plane=401\n'
  printf 'complete_usage_tokens=7,5,12\n'
  printf 'same_volume_restart=true\n'
  printf 'first_container_id=%s\n' "${first_container_id:0:12}"
  printf 'second_container_id=%s\n' "${second_container_id:0:12}"
  printf 'graceful_stop_exit=0\n'
} >"${summary_file}"

secret_labels=(auth_key encryption_key access_key credential_secret)
secret_values=("${auth_key}" "${encryption_key}" "${access_key}" "${credential_secret}")
for index in "${!secret_labels[@]}"; do
  secret_label="${secret_labels[${index}]}"
  secret_value="${secret_values[${index}]}"
  test -n "${secret_value}"
  if grep -R -F -q -- "${secret_value}" "${task_tmp}"; then
    exit 1
  fi
  printf '%s_secret_free=true\n' "${secret_label}" >>"${summary_file}"
done
printf 'two_round_logs_and_artifacts_secret_free=true\n' >>"${summary_file}"
printf 'secret_free=true\n' >>"${summary_file}"

for secret_value in "${secret_values[@]}"; do
  if grep -F -q -- "${secret_value}" "${summary_file}"; then
    exit 1
  fi
done

cat "${summary_file}" >&3
