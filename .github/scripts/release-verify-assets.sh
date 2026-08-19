#!/usr/bin/env bash
set -euo pipefail

# 校验一个发布资产目录是否与 .github/release-assets.txt 完全一致：
#   1. 目录内的普通文件清单必须逐字等于清单文件；
#   2. SHA256SUMS 必须恰好覆盖清单中除自身以外的全部资产；
#   3. 每一项校验和都必须通过。
# 清单文件是发布资产的唯一事实源，新增或改名资产只需要改动它。
#
# 用法：
#   release-verify-assets.sh <directory>
#   release-verify-assets.sh <directory> <trusted-checksum-file>
#
# 校验从远端下载的资产时必须传入第二个参数。此时目录内那份 SHA256SUMS 是不可信
# 输入，只用于身份比对，绝不执行；实际校验一律使用当前 run 生成的可信副本。

directory="${1:?release asset directory is required}"
trusted_checksums="${2:-}"
manifest="${RELEASE_ASSET_MANIFEST:-.github/release-assets.txt}"

absolute_path() {
  printf '%s/%s' "$(cd "$(dirname "$1")" && pwd)" "$(basename "$1")"
}

test -d "${directory}"
test -s "${manifest}"
manifest="$(absolute_path "${manifest}")"
if [[ -n "${trusted_checksums}" ]]; then
  test -s "${trusted_checksums}"
  trusted_checksums="$(absolute_path "${trusted_checksums}")"
fi

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

# 清单本身必须保持 C 序排序，否则后续所有 cmp 都会随 locale 漂移。
LC_ALL=C sort "${manifest}" >"${work}/expected"
cmp "${manifest}" "${work}/expected"

find "${directory}" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; |
  LC_ALL=C sort >"${work}/actual"
cmp "${work}/expected" "${work}/actual"

grep -vFx 'SHA256SUMS' "${work}/expected" >"${work}/expected-checksummed"

cd "${directory}"
awk '{print $2}' SHA256SUMS | LC_ALL=C sort >"${work}/actual-checksummed"
cmp "${work}/expected-checksummed" "${work}/actual-checksummed"

# release-assets-trusted-checksum:start
if [[ -n "${trusted_checksums}" ]]; then
  cmp "${trusted_checksums}" SHA256SUMS
  sha256sum --check "${trusted_checksums}"
else
  sha256sum --check SHA256SUMS
fi
# release-assets-trusted-checksum:end
