#!/usr/bin/env bash
set -euo pipefail

release_version="${RELEASE_VERSION:?RELEASE_VERSION is required}"
image_exact="${IMAGE_EXACT:?IMAGE_EXACT is required}"
image_beta="${IMAGE_BETA:-}"
image_major="${IMAGE_MAJOR:?IMAGE_MAJOR is required}"
promote_beta="${PROMOTE_BETA:?PROMOTE_BETA is required}"
promote_major="${PROMOTE_MAJOR:?PROMOTE_MAJOR is required}"
expected_revision="${EXPECTED_REVISION:?EXPECTED_REVISION is required}"
expected_ghcr_latest="${EXPECTED_GHCR_LATEST:?EXPECTED_GHCR_LATEST is required}"
expected_dockerhub_latest="${EXPECTED_DOCKERHUB_LATEST:?EXPECTED_DOCKERHUB_LATEST is required}"
output_file="${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"

semver_regex='^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
digest_regex='^sha256:[0-9a-f]{64}$'
alias_regex='^[0-9]+([.][0-9]+)?(-beta)?$'

[[ "${release_version}" =~ ${semver_regex} ]]
[[ "${image_exact}" == "${release_version#v}" ]]
[[ "${image_major}" =~ ^[0-9]+$ ]]
[[ -z "${image_beta}" || "${image_beta}" =~ ${alias_regex} ]]
[[ "${promote_beta}" == "true" || "${promote_beta}" == "false" ]]
[[ "${promote_major}" == "true" || "${promote_major}" == "false" ]]
[[ "${expected_revision}" =~ ^[0-9a-f]{40}$ ]]
[[ "${expected_ghcr_latest}" == "absent" || "${expected_ghcr_latest}" =~ ${digest_regex} ]]
[[ "${expected_dockerhub_latest}" == "absent" || "${expected_dockerhub_latest}" =~ ${digest_regex} ]]
if [[ "${promote_beta}" == "true" ]]; then
  test -n "${image_beta}"
fi

repositories=(ghcr.io/tbphp/gpt-load tbphp/gpt-load)
exact_digest=
for repository in "${repositories[@]}"; do
  exact="${repository}:${image_exact}"
  .github/scripts/release-verify-image-revision.sh \
    "${exact}" "${expected_revision}" >/dev/null
  digest="$(.github/scripts/release-image-digest.sh "${exact}")"
  [[ "${digest}" =~ ${digest_regex} ]]
  if [[ -z "${exact_digest}" ]]; then
    exact_digest="${digest}"
  else
    test "${digest}" = "${exact_digest}"
  fi
done

channel_is_current=false
promote_channel() {
  local alias="$1"
  local enabled="$2"
  local repository=
  local channel=
  local current_digest=
  local current_version=
  local comparison=
  local newer_exists=false
  local expected_digest=
  local source=
  local promoted_digest=
  local promoted_version=

  channel_is_current=false
  if [[ "${enabled}" != "true" ]]; then
    return
  fi

  for repository in "${repositories[@]}"; do
    channel="${repository}:${alias}"
    current_digest="$(.github/scripts/release-image-digest.sh "${channel}")"
    if [[ "${current_digest}" == "absent" ]]; then
      continue
    fi
    [[ "${current_digest}" =~ ${digest_regex} ]]
    current_version="$(.github/scripts/release-image-version.sh "${channel}")"
    comparison="$(
      python3 .github/scripts/release-compare-semver.py \
        "${current_version}" "${release_version}"
    )"
    [[ "${comparison}" == "-1" || "${comparison}" == "0" || "${comparison}" == "1" ]]
    if [[ "${comparison}" == "1" ]]; then
      newer_exists=true
    elif [[ "${comparison}" == "0" ]]; then
      expected_digest="${exact_digest}"
      if [[ "${current_digest}" != "${expected_digest}" ]]; then
        printf 'channel %s already has version %s with unexpected digest\n' \
          "${channel}" "${release_version}" >&2
        return 1
      fi
    fi
  done

  if [[ "${newer_exists}" == "true" ]]; then
    printf 'skip %s: a registry already points to a newer version than %s\n' \
      "${alias}" "${release_version}"
    return
  fi

  for repository in "${repositories[@]}"; do
    channel="${repository}:${alias}"
    expected_digest="${exact_digest}"
    current_digest="$(.github/scripts/release-image-digest.sh "${channel}")"
    if [[ "${current_digest}" != "${expected_digest}" ]]; then
      source="${repository}@${expected_digest}"
      docker buildx imagetools create \
        --tag "${channel}" \
        "${source}"
    fi
    promoted_digest="$(.github/scripts/release-image-digest.sh "${channel}")"
    test "${promoted_digest}" = "${expected_digest}"
    promoted_version="$(.github/scripts/release-image-version.sh "${channel}")"
    test "${promoted_version}" = "${release_version}"
  done
  channel_is_current=true
}

promote_channel "${image_beta}" "${promote_beta}"
beta_current="${channel_is_current}"
promote_channel "${image_major}" "${promote_major}"
major_current="${channel_is_current}"

test "$(.github/scripts/release-image-digest.sh ghcr.io/tbphp/gpt-load:latest)" = \
  "${expected_ghcr_latest}"
test "$(.github/scripts/release-image-digest.sh tbphp/gpt-load:latest)" = \
  "${expected_dockerhub_latest}"

{
  printf 'beta_current=%s\n' "${beta_current}"
  printf 'major_current=%s\n' "${major_current}"
} >>"${output_file}"
