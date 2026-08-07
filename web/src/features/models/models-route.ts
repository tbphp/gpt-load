import type { LocationQueryRaw } from 'vue-router'

const selectedPriceIDKey = 'selected_price_id'

export function parseSelectedPriceID(query: Record<string, unknown>): number | undefined {
  const raw = query[selectedPriceIDKey]
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

export function modelsQuery(selectedPriceID?: number): LocationQueryRaw {
  return selectedPriceID === undefined ? {} : { [selectedPriceIDKey]: String(selectedPriceID) }
}

export function sameModelsQuery(left: Record<string, unknown>, right: LocationQueryRaw): boolean {
  const leftKeys = Object.keys(left)
  const rightKeys = Object.keys(right)
  if (leftKeys.length !== rightKeys.length) return false

  return leftKeys.every(
    (key) =>
      Object.prototype.hasOwnProperty.call(right, key) &&
      typeof left[key] === 'string' &&
      left[key] === right[key],
  )
}
