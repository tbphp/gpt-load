#!/usr/bin/env bash
set -euo pipefail

required_inputs=(
  RELEASE_EXPECTED_SHA
  RELEASE_GITHUB_STATE
  RELEASE_GITHUB_TARGET_SHA
  RELEASE_GITHUB_ASSETS
  RELEASE_GHCR_STATE
  RELEASE_GHCR_DIGEST
  RELEASE_GHCR_REVISION
  RELEASE_DOCKERHUB_STATE
  RELEASE_DOCKERHUB_DIGEST
  RELEASE_DOCKERHUB_REVISION
)
for input in "${required_inputs[@]}"; do
  if ! printenv "${input}" >/dev/null; then
    printf 'missing required input: %s\n' "${input}" >&2
    exit 1
  fi
done

if [[ -z "${RELEASE_EXPECTED_SHA}" ]]; then
  printf 'RELEASE_EXPECTED_SHA must not be empty\n' >&2
  exit 1
fi

case "${RELEASE_GITHUB_STATE}" in
absent)
  if [[ -n "${RELEASE_GITHUB_TARGET_SHA}" ]] ||
    [[ "${RELEASE_GITHUB_ASSETS}" != "absent" ]]; then
    printf 'GitHub absent state contains present metadata\n' >&2
    exit 1
  fi
  ;;
present)
  if [[ -z "${RELEASE_GITHUB_TARGET_SHA}" ]] ||
    [[ "${RELEASE_GITHUB_ASSETS}" != "match" &&
      "${RELEASE_GITHUB_ASSETS}" != "mismatch" ]]; then
    printf 'GitHub present state has incomplete metadata\n' >&2
    exit 1
  fi
  ;;
*)
  printf 'invalid RELEASE_GITHUB_STATE\n' >&2
  exit 1
  ;;
esac

validate_registry() {
  local name="$1"
  local state="$2"
  local digest="$3"
  local revision="$4"

  case "${state}" in
  absent)
    if [[ "${digest}" != "absent" ]] || [[ -n "${revision}" ]]; then
      printf '%s absent state contains present metadata\n' "${name}" >&2
      return 1
    fi
    ;;
  present)
    if [[ -z "${digest}" || "${digest}" == "absent" || -z "${revision}" ]]; then
      printf '%s present state has incomplete metadata\n' "${name}" >&2
      return 1
    fi
    ;;
  *)
    printf 'invalid %s state\n' "${name}" >&2
    return 1
    ;;
  esac
}

validate_registry \
  "GHCR" \
  "${RELEASE_GHCR_STATE}" \
  "${RELEASE_GHCR_DIGEST}" \
  "${RELEASE_GHCR_REVISION}"
validate_registry \
  "Docker Hub" \
  "${RELEASE_DOCKERHUB_STATE}" \
  "${RELEASE_DOCKERHUB_DIGEST}" \
  "${RELEASE_DOCKERHUB_REVISION}"

if [[ "${RELEASE_GITHUB_STATE}" == "absent" &&
  "${RELEASE_GHCR_STATE}" == "absent" &&
  "${RELEASE_DOCKERHUB_STATE}" == "absent" ]]; then
  printf 'publication_state=fresh\n'
  printf 'write_mode=publish\n'
  exit 0
fi

conflict=false
if [[ "${RELEASE_GITHUB_STATE}" == "present" ]] &&
  {
    [[ "${RELEASE_GITHUB_TARGET_SHA}" != "${RELEASE_EXPECTED_SHA}" ]] ||
      [[ "${RELEASE_GITHUB_ASSETS}" != "match" ]]
  }; then
  conflict=true
fi
if [[ "${RELEASE_GHCR_STATE}" == "present" &&
  "${RELEASE_GHCR_REVISION}" != "${RELEASE_EXPECTED_SHA}" ]]; then
  conflict=true
fi
if [[ "${RELEASE_DOCKERHUB_STATE}" == "present" &&
  "${RELEASE_DOCKERHUB_REVISION}" != "${RELEASE_EXPECTED_SHA}" ]]; then
  conflict=true
fi
if [[ "${RELEASE_GHCR_STATE}" == "present" &&
  "${RELEASE_DOCKERHUB_STATE}" == "present" &&
  "${RELEASE_GHCR_DIGEST}" != "${RELEASE_DOCKERHUB_DIGEST}" ]]; then
  conflict=true
fi

if [[ "${conflict}" == "true" ]]; then
  printf 'publication_state=conflict\n'
  printf 'write_mode=blocked\n'
  exit 0
fi

if [[ "${RELEASE_GITHUB_STATE}" == "present" &&
  "${RELEASE_GHCR_STATE}" == "present" &&
  "${RELEASE_DOCKERHUB_STATE}" == "present" ]]; then
  printf 'publication_state=consistent\n'
  printf 'write_mode=verify\n'
  exit 0
fi

printf 'publication_state=partial\n'
printf 'write_mode=blocked\n'
