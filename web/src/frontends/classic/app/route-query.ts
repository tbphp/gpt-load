import type { LocationQuery, LocationQueryRaw } from 'vue-router'

const maxCollectionSearchCodePoints = 200

export function scalarRouteQuery(value: LocationQuery[string]): string | undefined {
  return typeof value === 'string' ? value : undefined
}

export function parsePositiveRouteInteger(value: LocationQuery[string]): number | undefined {
  const candidate = scalarRouteQuery(value)
  if (candidate === undefined || !/^[1-9]\d*$/u.test(candidate)) return undefined
  const parsed = Number(candidate)
  return Number.isSafeInteger(parsed) ? parsed : undefined
}

export function normalizeCollectionSearch(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return Array.from(trimmed).length <= maxCollectionSearchCodePoints ? trimmed : undefined
}

export function constrainCollectionSearch(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  return Array.from(trimmed).slice(0, maxCollectionSearchCodePoints).join('')
}

export function isCanonicalRouteQuery(query: LocationQuery, canonical: LocationQueryRaw): boolean {
  const actualKeys = Object.keys(query)
  const canonicalKeys = Object.keys(canonical)
  if (actualKeys.length !== canonicalKeys.length) return false
  return canonicalKeys.every((key) => sameRouteQueryValue(query[key], canonical[key]))
}

function sameRouteQueryValue(
  actual: LocationQuery[string],
  canonical: LocationQueryRaw[string],
): boolean {
  if (Array.isArray(actual) || Array.isArray(canonical)) {
    if (!Array.isArray(actual) || !Array.isArray(canonical)) return false
    return (
      actual.length === canonical.length &&
      actual.every((value, index) => value === canonical[index])
    )
  }
  return actual === canonical
}

export function parsePositiveRouteIntegerList(value: LocationQuery[string]): number[] {
  const candidate = scalarRouteQuery(value)
  if (candidate === undefined || candidate === '') return []

  const values = candidate.split(',').map((part) => Number(part))
  if (
    values.some((part) => !Number.isSafeInteger(part) || part <= 0) ||
    new Set(values).size !== values.length
  ) {
    return []
  }
  return [...values].sort((left, right) => left - right)
}

export function serializePositiveRouteIntegerList(values: readonly number[]): string | undefined {
  const normalized = [...new Set(values)]
    .filter((value) => Number.isSafeInteger(value) && value > 0)
    .sort((left, right) => left - right)
  return normalized.length > 0 ? normalized.join(',') : undefined
}
