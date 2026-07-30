import type { LocationQueryRaw } from 'vue-router'

export type GroupTab = 'keys' | 'models' | 'settings'
export type GroupKeyState = 'problem'

export function parsePositiveId(raw: unknown): number | undefined {
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

export function normalizeGroupTab(raw: unknown): GroupTab {
  return raw === 'models' || raw === 'settings' || raw === 'keys' ? raw : 'keys'
}

export function normalizeGroupQuery(query: Record<string, unknown>): LocationQueryRaw {
  const tab = normalizeGroupTab(query.tab)
  if (tab === 'keys' && query.key_state === 'problem') {
    return { tab, key_state: 'problem' }
  }
  return { tab }
}
