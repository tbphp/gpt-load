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
  return canonicalKeys.every((key) => query[key] === canonical[key])
}
