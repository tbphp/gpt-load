#!/usr/bin/env bash
set -euo pipefail

# 断言多架构镜像的 linux/amd64 与 linux/arm64 两个 config 标签
# org.opencontainers.image.revision 都等于期望的 commit SHA。
#
# registry 返回的标签值是不可信输入，只允许在 jq 比较内部使用：进入 shell 的
# 永远只有受信的 ${expected} 或字面量 mismatch，实际读到的值仅直接写入 stderr。
#
# 退出码约定，调用方据此区分「确实不匹配」与「无法完成检查」：
#   0 = 两个架构都匹配，stdout 输出该 revision
#   1 = 镜像可读但 revision 不匹配，stderr 输出实际读到的标签
#   2 = 无法读取镜像清单（网络、认证或镜像缺失），stderr 输出原始错误

image="${1:?image is required}"
expected="${2:?expected revision is required}"

if ! inspection="$(
  docker buildx imagetools inspect "${image}" --format '{{json .}}' 2>&1
)"; then
  printf '%s\n' "${inspection}" >&2
  exit 2
fi

# release-image-revision-validation:start
revision=mismatch
if jq -e \
  --arg expected "${expected}" \
  '[
    .image["linux/amd64"].config.Labels[
      "org.opencontainers.image.revision"
    ],
    .image["linux/arm64"].config.Labels[
      "org.opencontainers.image.revision"
    ]
  ] | all(type == "string" and . == $expected)' \
  <<<"${inspection}" >/dev/null 2>&1; then
  revision="${expected}"
fi
# release-image-revision-validation:end

if [[ "${revision}" == "mismatch" ]]; then
  printf 'image revision mismatch for %s (expected %s), actual labels: ' \
    "${image}" "${expected}" >&2
  jq -c '[
    .image["linux/amd64"].config.Labels[
      "org.opencontainers.image.revision"
    ],
    .image["linux/arm64"].config.Labels[
      "org.opencontainers.image.revision"
    ]
  ]' <<<"${inspection}" >&2 2>/dev/null || printf 'unreadable\n' >&2
  exit 1
fi

printf '%s' "${revision}"
