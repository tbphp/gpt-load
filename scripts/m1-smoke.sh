#!/usr/bin/env bash
set -euo pipefail

for command_name in curl jq; do
  command -v "${command_name}" >/dev/null || {
    echo "missing command: ${command_name}" >&2
    exit 1
  }
done

for name in GPT_LOAD_URL AUTH_KEY UPSTREAM_URL UPSTREAM_KEY MODEL; do
  [[ -n "${!name:-}" ]] || {
    echo "missing environment variable: ${name}" >&2
    exit 1
  }
done

base_url="${GPT_LOAD_URL%/}"
import_payload="$(jq -n \
  --arg url "${UPSTREAM_URL}" \
  --arg key "${UPSTREAM_KEY}" \
  --arg model "${MODEL}" \
  '{upstream_url:$url,protocols:["openai"],keys:$key,models:[{id:$model,alias:""}]}')"
import_response="$(curl -fsS \
  -H "Authorization: Bearer ${AUTH_KEY}" \
  -H 'Content-Type: application/json' \
  -d "${import_payload}" \
  "${base_url}/api/import")"
group_id="$(jq -er '.data.group_id' <<<"${import_response}")"

curl -fsS \
  -H "Authorization: Bearer ${AUTH_KEY}" \
  "${base_url}/api/groups" \
  | jq -e --argjson id "${group_id}" '.data[] | select(.id == $id)' >/dev/null

access_payload="$(jq -n \
  --arg model "${MODEL}" \
  --argjson group "${group_id}" \
  '{name:"m1-smoke",filters:{groups:[$group],protocols:["openai"],models:[$model]}}')"
access_response="$(curl -fsS \
  -H "Authorization: Bearer ${AUTH_KEY}" \
  -H 'Content-Type: application/json' \
  -d "${access_payload}" \
  "${base_url}/api/access-keys")"
access_key="$(jq -er '.data.key' <<<"${access_response}")"

chat_payload="$(jq -n \
  --arg model "${MODEL}" \
  '{model:$model,messages:[{role:"user",content:"Reply with OK"}]}')"
curl -fsS \
  -H "Authorization: Bearer ${access_key}" \
  -H 'Content-Type: application/json' \
  -d "${chat_payload}" \
  "${base_url}/v1/chat/completions" \
  | jq -e . >/dev/null

printf 'M1 S7 smoke passed for group_id=%s\n' "${group_id}"
