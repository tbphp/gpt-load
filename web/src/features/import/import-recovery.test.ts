import type { ImportDraft } from './model-draft'
import {
  createImportRecoveryService,
  importRecoveryStorageKey,
  importRecoveryTtlMs,
} from './import-recovery'

const draft: ImportDraft = {
  mode: 'new',
  step: 2,
  preset_id: 'custom',
  name: 'Canary',
  upstream_url: 'https://api.example.com',
  protocols: ['openai'],
  keys: 'UPSTREAM_KEY_CANARY_91e7',
  header_rules: { set: { 'X-Secret': 'HEADER_CANARY_57aa' }, remove: ['X-Debug'] },
  models: [{ id: 'gpt-4o', alias: '', selected: true }],
}

const existingDraft = {
  mode: 'existing' as const,
  group_id: 7,
  keys: 'EXISTING_KEY_CANARY_a17c',
}

function memoryStorage(events: string[] = []): Storage {
  const values = new Map<string, string>()
  return {
    get length() {
      return values.size
    },
    clear() {
      values.clear()
    },
    getItem(key) {
      events.push(`get:${key}`)
      return values.get(key) ?? null
    },
    key(index) {
      return [...values.keys()][index] ?? null
    },
    removeItem(key) {
      events.push(`remove:${key}`)
      values.delete(key)
    },
    setItem(key, value) {
      events.push(`set:${key}`)
      values.set(key, value)
    },
  }
}

function createHarness(storage?: Storage, now = 10_000) {
  const timers = new Map<number, () => void>()
  let nextTimer = 1
  const service = createImportRecoveryService({
    storage,
    now: () => now,
    setTimer(callback) {
      const id = nextTimer++
      timers.set(id, callback)
      return id as ReturnType<typeof setTimeout>
    },
    clearTimer(timer) {
      timers.delete(timer as number)
    },
  })
  return { service, timers }
}

describe('import recovery', () => {
  it('uses the approved key, version 1, exact 15-minute TTL, and only captures a registered draft', () => {
    expect(importRecoveryStorageKey).toBe('gpt-load.import-reauth-draft')
    expect(importRecoveryTtlMs).toBe(15 * 60 * 1_000)
    const storage = memoryStorage()
    const { service, timers } = createHarness(storage)
    expect(service.captureForUnauthorized()).toBe('no-active-draft')

    const unregister = service.register(() => draft)
    expect(storage.getItem(importRecoveryStorageKey)).toBeNull()
    expect(service.captureForUnauthorized()).toBe('stored')
    expect(JSON.parse(storage.getItem(importRecoveryStorageKey) ?? '')).toEqual({
      version: 1,
      expires_at: 910_000,
      draft,
    })
    expect(timers.size).toBe(1)

    unregister()
    service.dispose()
    expect(timers.size).toBe(0)
  })

  it('captures and restores the existing-mode discriminated union branch', () => {
    const storage = memoryStorage()
    const { service } = createHarness(storage)
    service.register(() => existingDraft)

    expect(service.captureForUnauthorized()).toBe('stored')
    expect(service.consume()).toEqual(existingDraft)
    expect(storage.getItem(importRecoveryStorageKey)).toBeNull()
  })

  it('rejects malformed or non-positive existing-mode Group identities after confirmed removal', () => {
    for (const groupID of [0, -1, 1.5, '7']) {
      const storage = memoryStorage()
      storage.setItem(
        importRecoveryStorageKey,
        JSON.stringify({
          version: 1,
          expires_at: 910_000,
          draft: { mode: 'existing', group_id: groupID, keys: 'canary' },
        }),
      )
      const { service } = createHarness(storage)

      expect(service.consume()).toBeNull()
      expect(storage.getItem(importRecoveryStorageKey)).toBeNull()
    }
  })

  it('consumes in get, remove, confirm-absent, then parse order and leaves no secret in storage', () => {
    const events: string[] = []
    const storage = memoryStorage(events)
    const { service } = createHarness(storage)
    service.register(() => draft)
    service.captureForUnauthorized()
    events.length = 0
    const originalParse = JSON.parse
    const parse = vi.spyOn(JSON, 'parse').mockImplementation((value) => {
      events.push('parse')
      return originalParse(value)
    })

    expect(service.consume()).toEqual(draft)
    expect(events).toEqual([
      `get:${importRecoveryStorageKey}`,
      `remove:${importRecoveryStorageKey}`,
      `get:${importRecoveryStorageKey}`,
      'parse',
    ])
    expect(storage.getItem(importRecoveryStorageKey)).toBeNull()
    parse.mockRestore()
  })

  it('fails closed without parsing when removal throws or absence cannot be confirmed', () => {
    const raw = JSON.stringify({ version: 1, expires_at: 910_000, draft })
    let gets = 0
    const storage = {
      ...memoryStorage(),
      getItem: vi.fn(() => {
        gets += 1
        return raw
      }),
      removeItem: vi.fn(() => {
        throw new Error('denied')
      }),
    }
    const parse = vi.spyOn(JSON, 'parse')
    const { service } = createHarness(storage)

    expect(service.consume()).toBeNull()
    expect(gets).toBe(1)
    expect(parse).not.toHaveBeenCalled()
    parse.mockRestore()
  })

  it('returns storage-unavailable without throwing and expires captured drafts by timer', () => {
    const denied = {
      ...memoryStorage(),
      setItem: () => {
        throw new Error('denied')
      },
    }
    expect(createHarness(denied).service.captureForUnauthorized()).toBe('no-active-draft')
    const deniedHarness = createHarness(denied)
    deniedHarness.service.register(() => draft)
    expect(deniedHarness.service.captureForUnauthorized()).toBe('storage-unavailable')

    const storage = memoryStorage()
    const { service, timers } = createHarness(storage)
    service.register(() => draft)
    service.captureForUnauthorized()
    ;[...timers.values()][0]?.()
    expect(storage.getItem(importRecoveryStorageKey)).toBeNull()
  })

  it('removes an empty incompatible record before rejecting it', () => {
    const storage = memoryStorage()
    storage.setItem(importRecoveryStorageKey, '')
    const { service } = createHarness(storage)
    expect(service.consume()).toBeNull()
    expect(storage.getItem(importRecoveryStorageKey)).toBeNull()
  })

  it('discards expired and incompatible records after confirmed removal', () => {
    for (const record of [
      { version: 1, expires_at: 9_999, draft },
      { version: 2, expires_at: 910_000, draft },
    ]) {
      const storage = memoryStorage()
      storage.setItem(importRecoveryStorageKey, JSON.stringify(record))
      const { service } = createHarness(storage)
      expect(service.consume()).toBeNull()
      expect(storage.getItem(importRecoveryStorageKey)).toBeNull()
    }
  })
})
