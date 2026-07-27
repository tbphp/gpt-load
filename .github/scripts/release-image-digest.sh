#!/usr/bin/env bash
set -euo pipefail

image="${1:?image is required}"
output=
if output="$(
  docker buildx imagetools inspect \
    "${image}" \
    --format '{{.Manifest.Digest}}' 2>&1
)"; then
  test -n "${output}"
  printf '%s' "${output}"
  exit 0
fi

if grep -Eqi 'manifest unknown|manifest_unknown' <<<"${output}" ||
  {
    grep -Fqi -- "${image}" <<<"${output}" &&
      grep -Eqi 'not found|(^|[^0-9])404([^0-9]|$)' <<<"${output}"
  }; then
  printf 'absent'
  exit 0
fi

printf '%s\n' "${output}" >&2
exit 1
