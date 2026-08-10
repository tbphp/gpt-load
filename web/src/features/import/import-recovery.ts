import { inject, type InjectionKey } from 'vue'

import type { ImportRecoveryDraft, ModelDraftItem } from './model-draft'

export const importRecoveryStorageKey = 'gpt-load.import-reauth-draft'
export const importRecoveryTtlMs = 15 * 60 * 1_000

export interface ImportRecoveryDependencies {
  storage?: Storage
  now(): number
  setTimer(callback: () => void, delayMs: number): ReturnType<typeof setTimeout>
  clearTimer(timer: ReturnType<typeof setTimeout>): void
}

export interface ImportRecoveryService {
  register(getDraft: () => ImportRecoveryDraft | null): () => void
  captureForUnauthorized(): 'stored' | 'no-active-draft' | 'storage-unavailable'
  consume(): ImportRecoveryDraft | null
  clear(): void
  sweep(): void
  dispose(): void
}

interface ImportRecoveryRecord {
  version: 5
  expires_at: number
  draft: ImportRecoveryDraft
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function hasOnlyFields(value: Record<string, unknown>, fields: readonly string[]): boolean {
  const allowed = new Set(fields)
  return Object.keys(value).every((field) => allowed.has(field))
}

function isModel(value: unknown): value is ModelDraftItem {
  return (
    isRecord(value) &&
    hasOnlyFields(value, [
      'id',
      'name',
      'sources',
      'pricing_status',
      'alias',
      'alias_enabled',
      'editable_id',
      'key',
    ]) &&
    typeof value.id === 'string' &&
    typeof value.name === 'string' &&
    Array.isArray(value.sources) &&
    value.sources.length <= 2 &&
    value.sources.every((source) => source === 'catalog' || source === 'live') &&
    new Set(value.sources).size === value.sources.length &&
    (value.pricing_status === 'pending' || value.pricing_status === 'configured') &&
    typeof value.alias === 'string' &&
    typeof value.alias_enabled === 'boolean' &&
    (value.editable_id === undefined || typeof value.editable_id === 'boolean') &&
    typeof value.key === 'number' &&
    Number.isSafeInteger(value.key) &&
    value.key > 0
  )
}

function isChannelID(value: unknown): value is string {
  return typeof value === 'string' && /^[a-z][a-z0-9_]*$/u.test(value) && value.length <= 100
}

function isChannelParams(value: unknown): value is Record<string, string> {
  return (
    isRecord(value) &&
    Object.entries(value).every(
      ([key, item]) => /^[a-z][a-z0-9_]*$/u.test(key) && typeof item === 'string',
    )
  )
}

function isNewImportDraft(value: Record<string, unknown>): boolean {
  if (!(
    hasOnlyFields(value, ['mode', 'channel_id', 'params', 'name', 'credentials', 'models']) &&
    value.mode === 'new' &&
    isChannelID(value.channel_id) &&
    isChannelParams(value.params) &&
    typeof value.name === 'string' &&
    typeof value.credentials === 'string' &&
    Array.isArray(value.models) &&
    value.models.every(isModel)
  )) {
    return false
  }
  const models = value.models as ModelDraftItem[]
  return new Set(models.map(({ key }) => key)).size === models.length
}

function isExistingImportDraft(value: Record<string, unknown>): boolean {
  return (
    hasOnlyFields(value, ['mode', 'group_id', 'credentials']) &&
    value.mode === 'existing' &&
    (value.group_id === null ||
      (typeof value.group_id === 'number' &&
        Number.isSafeInteger(value.group_id) &&
        value.group_id > 0)) &&
    typeof value.credentials === 'string'
  )
}

function isImportDraft(value: unknown): value is ImportRecoveryDraft {
  return isRecord(value) && (isNewImportDraft(value) || isExistingImportDraft(value))
}

function parseRecoveryRecord(raw: string): ImportRecoveryRecord | null {
  try {
    const value: unknown = JSON.parse(raw)
    if (
      !isRecord(value) ||
      !hasOnlyFields(value, ['version', 'expires_at', 'draft']) ||
      value.version !== 5 ||
      typeof value.expires_at !== 'number' ||
      !Number.isFinite(value.expires_at) ||
      !isImportDraft(value.draft)
    ) {
      return null
    }
    return value as unknown as ImportRecoveryRecord
  } catch {
    return null
  }
}

function safeGet(storage: Storage | undefined, key: string): string | null {
  try {
    return storage?.getItem(key) ?? null
  } catch {
    return null
  }
}

function removeAndConfirm(storage: Storage, key: string): boolean {
  try {
    storage.removeItem(key)
    return storage.getItem(key) === null
  } catch {
    return false
  }
}

export function createImportRecoveryService(
  deps: ImportRecoveryDependencies,
): ImportRecoveryService {
  let activeGetter: (() => ImportRecoveryDraft | null) | undefined
  let expiryTimer: ReturnType<typeof setTimeout> | undefined

  function cancelTimer(): void {
    if (expiryTimer !== undefined) {
      deps.clearTimer(expiryTimer)
      expiryTimer = undefined
    }
  }

  function clear(): void {
    cancelTimer()
    try {
      deps.storage?.removeItem(importRecoveryStorageKey)
    } catch {
      // Cleanup remains best effort when browser storage is denied.
    }
  }

  function scheduleExpiry(delayMs: number): void {
    cancelTimer()
    expiryTimer = deps.setTimer(
      () => {
        expiryTimer = undefined
        try {
          deps.storage?.removeItem(importRecoveryStorageKey)
        } catch {
          // Expiry cannot weaken the in-memory authentication cleanup path.
        }
      },
      Math.max(0, delayMs),
    )
  }

  function captureForUnauthorized(): ReturnType<ImportRecoveryService['captureForUnauthorized']> {
    let draft: ImportRecoveryDraft | null
    try {
      draft = activeGetter?.() ?? null
    } catch {
      return 'no-active-draft'
    }
    if (!draft) return 'no-active-draft'
    if (!deps.storage) return 'storage-unavailable'

    const record: ImportRecoveryRecord = {
      version: 5,
      expires_at: deps.now() + importRecoveryTtlMs,
      draft,
    }
    try {
      deps.storage.setItem(importRecoveryStorageKey, JSON.stringify(record))
    } catch {
      return 'storage-unavailable'
    }
    scheduleExpiry(importRecoveryTtlMs)
    return 'stored'
  }

  function consume(): ImportRecoveryDraft | null {
    const raw = safeGet(deps.storage, importRecoveryStorageKey)
    if (
      raw === null ||
      !deps.storage ||
      !removeAndConfirm(deps.storage, importRecoveryStorageKey)
    ) {
      return null
    }
    cancelTimer()
    const parsed = parseRecoveryRecord(raw)
    return parsed && parsed.expires_at > deps.now() ? parsed.draft : null
  }

  function sweep(): void {
    const raw = safeGet(deps.storage, importRecoveryStorageKey)
    if (raw === null) {
      cancelTimer()
      return
    }
    const parsed = parseRecoveryRecord(raw)
    if (!parsed || parsed.expires_at <= deps.now()) {
      clear()
      return
    }
    scheduleExpiry(parsed.expires_at - deps.now())
  }

  return {
    register(getDraft) {
      activeGetter = getDraft
      return () => {
        if (activeGetter === getDraft) activeGetter = undefined
      }
    },
    captureForUnauthorized,
    consume,
    clear,
    sweep,
    dispose() {
      activeGetter = undefined
      cancelTimer()
    },
  }
}

export const importRecoveryKey: InjectionKey<ImportRecoveryService> = Symbol('import-recovery')

export function useImportRecovery(): ImportRecoveryService {
  const service = inject(importRecoveryKey)
  if (!service) throw new Error('IMPORT_RECOVERY_NOT_PROVIDED')
  return service
}
