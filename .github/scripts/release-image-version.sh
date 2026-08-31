#!/usr/bin/env bash
set -euo pipefail

# 读取多架构镜像内的软件版本标签。registry 返回值属于不可信输入，只有两个
# 目标架构完全一致且满足严格 SemVer（允许 Git 的 v 前缀）时才输出。

image="${1:?image is required}"

if ! inspection="$(
  docker buildx imagetools inspect "${image}" --format '{{json .}}' 2>&1
)"; then
  printf '%s\n' "${inspection}" >&2
  exit 2
fi

semver_regex='^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
if ! version="$(
  jq -er \
    --arg semver_regex "${semver_regex}" \
    '[
      .image["linux/amd64"].config.Labels[
        "org.opencontainers.image.version"
      ],
      .image["linux/arm64"].config.Labels[
        "org.opencontainers.image.version"
      ]
    ]
    | if
        length == 2 and
        all(type == "string") and
        .[0] == .[1] and
        (.[0] | test($semver_regex))
      then .[0]
      else error("image version labels are missing, invalid, or inconsistent")
      end' \
    <<<"${inspection}" 2>/dev/null
)"; then
  printf 'image version labels are missing, invalid, or inconsistent for %s\n' \
    "${image}" >&2
  exit 1
fi

printf '%s' "${version}"
