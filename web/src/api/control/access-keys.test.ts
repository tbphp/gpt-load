import type { ApiClient } from '@/api/client'

import {
  createAccessKey,
  deleteAccessKey,
  listAccessKeys,
  listAccessKeyOptions,
  revealAccessKey,
  updateAccessKey,
  type CreateAccessKeyRequest,
  type UpdateAccessKeyRequest,
} from './access-keys'

const createBody: CreateAccessKeyRequest = {
  name: 'client',
  filters: { groups: [], protocols: [], models: [] },
  rpm_limit: 0,
}

const updateBody: UpdateAccessKeyRequest = {
  status: 'disabled',
  filters: { groups: [7], protocols: ['openai-response'], models: ['gpt-4.1'] },
}

describe('AccessKey control API', () => {
  it('uses exact metadata/options/reveal and CRUD contracts with operation identity', async () => {
    const signal = new AbortController().signal
    const metadata = {
      id: 9,
      name: 'client',
      masked_key: 'sk-gl-••••••••cafe',
      status: 'active',
      filters: { groups: [], protocols: [], models: [] },
      rpm_limit: 0,
      created_at: '2026-07-28T00:00:00Z',
      updated_at: '2026-07-28T00:00:00Z',
    }
    const request = vi.fn(async (path: string, options?: { method?: string }) => {
      if (path === '/api/access-keys/options') {
        return [{ id: 9, name: 'client', status: 'active' }]
      }
      if (path === '/api/access-keys/9/reveal') {
        return { id: 9, key: 'sk-gl-REVEALED', revealed_at: '2026-07-28T00:00:00Z' }
      }
      if (path === '/api/access-keys' && options?.method === 'POST') {
        return { ...metadata, key: 'sk-gl-CREATED', replayed: false }
      }
      if (path === '/api/access-keys') return [metadata]
      if (path === '/api/access-keys/9') return metadata
      return undefined
    })
    const client: ApiClient = { request: request as ApiClient['request'] }
    const operationID = '00000000-0000-4000-8000-000000000001'

    await listAccessKeys(client, signal)
    await listAccessKeyOptions(client, signal)
    await createAccessKey(client, createBody, operationID, signal)
    await revealAccessKey(client, 9, signal)
    await updateAccessKey(client, 9, updateBody, signal)
    await deleteAccessKey(client, 9, signal)

    expect(request.mock.calls).toEqual([
      ['/api/access-keys', { method: 'GET', signal }],
      ['/api/access-keys/options', { method: 'GET', signal }],
      [
        '/api/access-keys',
        {
          method: 'POST',
          headers: { 'Idempotency-Key': operationID },
          json: createBody,
          signal,
        },
      ],
      ['/api/access-keys/9/reveal', { method: 'POST', signal }],
      ['/api/access-keys/9', { method: 'PUT', json: updateBody, signal }],
      ['/api/access-keys/9', { method: 'DELETE', signal }],
    ])
    expect(createBody).not.toHaveProperty('status')
  })

  it('projects an allowlisted metadata list and never retains an unexpected plaintext field', async () => {
    const request = vi.fn().mockResolvedValue([
      {
        id: 12,
        name: 'Client',
        key: 'sk-gl-CANARY',
        masked_key: 'sk-gl-••••••••cafe',
        status: 'active',
        filters: { groups: [], protocols: [], models: [] },
        rpm_limit: 0,
        created_at: '2026-07-28T00:00:00Z',
        updated_at: '2026-07-28T01:00:00Z',
        secret_debug: 'DO_NOT_CACHE',
      },
    ]) as ApiClient['request']
    const client: ApiClient = { request }

    const list = await listAccessKeys(client)
    expect(list).toEqual([
      {
        id: 12,
        name: 'Client',
        masked_key: 'sk-gl-••••••••cafe',
        status: 'active',
        filters: { groups: [], protocols: [], models: [] },
        rpm_limit: 0,
        created_at: '2026-07-28T00:00:00Z',
        updated_at: '2026-07-28T01:00:00Z',
      },
    ])
    expect(JSON.stringify(list)).not.toContain('sk-gl-CANARY')
    expect(JSON.stringify(list)).not.toContain('DO_NOT_CACHE')
  })

  it.each([
    'sk-gl-0123456789abcdef0123456789abcdef',
    'sk-gl-••••••••0123456789abcdef',
    'enc:v1:sk-gl-••••••••cafe',
    'sk-gl-••••••••CAFE',
  ])(
    'rejects a non-canonical masked_key before it can enter metadata cache: %s',
    async (maskedKey) => {
      const request = vi.fn().mockResolvedValue([
        {
          id: 12,
          name: 'Client',
          masked_key: maskedKey,
          status: 'active',
          filters: { groups: [], protocols: [], models: [] },
          rpm_limit: 0,
          created_at: '2026-07-28T00:00:00Z',
          updated_at: '2026-07-28T01:00:00Z',
        },
      ]) as ApiClient['request']

      await expect(listAccessKeys({ request })).rejects.toThrow('INVALID_API_RESPONSE')
    },
  )

  it('reads safe options and reveals plaintext only through the reveal endpoint', async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce([{ id: 12, name: 'Client', status: 'active', key: 'DO_NOT_KEEP' }])
      .mockResolvedValueOnce({
        id: 12,
        key: 'sk-gl-REVEAL-CANARY',
        revealed_at: '2026-07-28T01:00:00Z',
        debug: 'ignored',
      }) as ApiClient['request']
    const client: ApiClient = { request }

    const options = await listAccessKeyOptions(client)
    expect(options).toEqual([{ id: 12, name: 'Client', status: 'active' }])
    expect(JSON.stringify(options)).not.toContain('DO_NOT_KEEP')
    expect(await revealAccessKey(client, 12)).toEqual({
      id: 12,
      key: 'sk-gl-REVEAL-CANARY',
      revealed_at: '2026-07-28T01:00:00Z',
    })
  })
})
