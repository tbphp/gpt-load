export type GroupTab = 'keys' | 'models' | 'settings'

export function parsePositiveId(raw: unknown): number | undefined {
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

export function normalizeGroupTab(raw: unknown): GroupTab {
  return raw === 'models' || raw === 'settings' || raw === 'keys' ? raw : 'keys'
}
