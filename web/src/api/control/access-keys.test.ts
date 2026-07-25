import type { ApiClient } from '@/api/client'

import {
  createAccessKey,
  deleteAccessKey,
  listAccessKeys,
  listAccessKeyOptions,
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
  it('uses the exact list and CRUD paths, methods, bodies, and AbortSignals', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue(undefined)
    const client: ApiClient = { request: request as ApiClient['request'] }

    await listAccessKeys(client, signal)
    await createAccessKey(client, createBody, signal)
    await updateAccessKey(client, 9, updateBody, signal)
    await deleteAccessKey(client, 9, signal)

    expect(request.mock.calls).toEqual([
      ['/api/access-keys', { method: 'GET', signal }],
      ['/api/access-keys', { method: 'POST', json: createBody, signal }],
      ['/api/access-keys/9', { method: 'PUT', json: updateBody, signal }],
      ['/api/access-keys/9', { method: 'DELETE', signal }],
    ])
    expect(createBody).not.toHaveProperty('status')
  })

  it('projects safe AccessKey options without retaining plaintext credentials', async () => {
    const request = vi.fn().mockResolvedValue([
      {
        id: 12,
        name: 'Client',
        key: 'sk-gl-CANARY',
        status: 'active',
        filters: { groups: [], protocols: [], models: [] },
        rpm_limit: 0,
      },
    ]) as ApiClient['request']
    const client: ApiClient = { request }

    expect(await listAccessKeyOptions(client)).toEqual([
      { id: 12, name: 'Client', status: 'active' },
    ])
    expect(JSON.stringify(await listAccessKeyOptions(client))).not.toContain('sk-gl-CANARY')
  })
})
