import type { ApiClient } from '@/api/client'

import {
  createGroup,
  discoverGroupModels,
  discoverModels,
  importGroupKeys,
  isUpstreamUrlConflictData,
  listGroups,
  replaceGroupModels,
} from './groups'

describe('Group control API', () => {
  it('requests GET /api/groups and forwards AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue([]) as ApiClient['request']
    await listGroups({ request }, signal)
    expect(request).toHaveBeenCalledWith('/api/groups', { method: 'GET', signal })
  })

  it('discovers models with the exact secret-bearing body and feature AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue({ models: ['gpt-4o'] }) as ApiClient['request']
    const body = {
      upstream_url: 'https://api.example.com',
      protocols: ['openai'] as const,
      keys: 'raw-key\nraw-key',
      config: { header_rules: { set: { 'X-Test': 'secret' }, remove: [] } },
    }

    await discoverModels({ request }, body, signal)

    expect(request).toHaveBeenCalledWith('/api/models/discover', {
      method: 'POST',
      json: body,
      signal,
    })
  })

  it('discovers existing Group model candidates with the exact empty-body endpoint', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue({ models: ['gpt-4o'] }) as ApiClient['request']

    await discoverGroupModels({ request }, 7, signal)

    expect(request).toHaveBeenCalledWith('/api/groups/7/models/discover', {
      method: 'POST',
      signal,
    })
  })

  it('replaces the exact full normalized Group model list', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue({ id: 7 }) as ApiClient['request']
    const models = [
      { id: 'gpt-4o', alias: 'public' },
      { id: 'o3', alias: '' },
    ]

    await replaceGroupModels({ request }, 7, { models }, signal)

    expect(request).toHaveBeenCalledWith('/api/groups/7/models', {
      method: 'PUT',
      json: { models },
      signal,
    })
  })

  it('creates a Group and imports raw keys through separate exact endpoints', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue({ group_id: 7 }) as ApiClient['request']
    const createBody = {
      name: 'Primary',
      upstream_url: 'https://api.example.com',
      protocols: ['openai'] as const,
      models: [{ id: 'gpt-4o', alias: 'primary' }],
      config: { header_rules: { set: {}, remove: [] } },
      keys: 'raw-key',
      confirm_same_upstream_url: false,
    }

    await createGroup({ request }, createBody, signal)
    await importGroupKeys({ request }, 7, { keys: 'raw-key' }, signal)

    expect(request).toHaveBeenNthCalledWith(1, '/api/groups', {
      method: 'POST',
      json: createBody,
      signal,
    })
    expect(request).toHaveBeenNthCalledWith(2, '/api/groups/7/keys/import', {
      method: 'POST',
      json: { keys: 'raw-key' },
      signal,
    })
  })

  it('accepts only structured UPSTREAM_URL_CONFLICT group identities', () => {
    expect(isUpstreamUrlConflictData({ groups: [{ id: 7, name: 'Existing' }] })).toBe(true)
    expect(isUpstreamUrlConflictData({ groups: [{ id: 0, name: 'Existing' }] })).toBe(false)
    expect(isUpstreamUrlConflictData({ groups: [{ id: 7, name: 9 }] })).toBe(false)
    expect(isUpstreamUrlConflictData({ group: [] })).toBe(false)
  })
})
