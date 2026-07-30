import type { ApiClient } from '@/api/client'

import {
  createGroup,
  discoverGroupModels,
  discoverModels,
  importGroupKeys,
  isGroupInUseData,
  isUpstreamUrlConflictData,
  listGroups,
  replaceGroupModels,
} from './groups'

describe('Group control API', () => {
  const detail = {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai-chat-completions'] as const,
    models: [{ id: 'gpt-4o', alias: 'public' }],
    enabled: true,
    validation_model: null,
    weight_manual: null,
    config: {},
    effective_config: {
      connect_timeout: 15,
      first_byte_timeout: 120,
      request_timeout: 600,
      stream_idle_timeout: 300,
      header_rules: { set: {}, remove: [] },
      inject_usage_options: false,
    },
    key_count: 1,
  }

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
      protocols: ['openai-chat-completions'] as const,
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
    const request = vi.fn().mockResolvedValue(detail) as ApiClient['request']
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
    const request = vi
      .fn()
      .mockResolvedValueOnce({
        group_id: 7,
        group_name: 'Primary',
        keys_added: 1,
        keys_duplicated: 0,
        models: [{ id: 'gpt-4o', alias: 'primary' }],
      })
      .mockResolvedValueOnce({
        group_id: 7,
        keys_added: 1,
        keys_duplicated: 0,
      }) as ApiClient['request']
    const createOperationID = '318f47a2-9c35-4d6e-8b1a-1234567890ab'
    const importOperationID = 'd4ba3f42-67bc-4b5b-b594-1234567890ab'
    const createBody = {
      name: 'Primary',
      upstream_url: 'https://api.example.com',
      protocols: ['openai-chat-completions'] as const,
      models: [{ id: 'gpt-4o', alias: 'primary' }],
      config: { header_rules: { set: {}, remove: [] } },
      keys: 'raw-key',
      confirm_same_upstream_url: false,
    }

    await createGroup({ request }, createBody, createOperationID, signal)
    await importGroupKeys({ request }, 7, { keys: 'raw-key' }, importOperationID, signal)

    expect(request).toHaveBeenNthCalledWith(1, '/api/groups', {
      method: 'POST',
      headers: { 'Idempotency-Key': createOperationID },
      json: createBody,
      signal,
    })
    expect(request).toHaveBeenNthCalledWith(2, '/api/groups/7/keys/import', {
      method: 'POST',
      headers: { 'Idempotency-Key': importOperationID },
      json: { keys: 'raw-key' },
      signal,
    })
  })

  it('accepts only displayable structured conflict and in-use identities', () => {
    expect(isUpstreamUrlConflictData({ groups: [{ id: 7, name: 'Existing' }] })).toBe(true)
    expect(isUpstreamUrlConflictData({ groups: [{ id: 0, name: 'Existing' }] })).toBe(false)
    expect(
      isUpstreamUrlConflictData({
        groups: [{ id: Number.MAX_SAFE_INTEGER + 1, name: 'Existing' }],
      }),
    ).toBe(false)
    expect(isUpstreamUrlConflictData({ groups: [{ id: 7, name: '   ' }] })).toBe(false)
    expect(isUpstreamUrlConflictData({ groups: [] })).toBe(false)
    expect(isGroupInUseData({ access_keys: [{ id: 7, name: 'Consumer' }] })).toBe(true)
    expect(isGroupInUseData({ access_keys: [] })).toBe(false)
    expect(
      isGroupInUseData({ access_keys: [{ id: Number.MAX_SAFE_INTEGER + 1, name: 'Consumer' }] }),
    ).toBe(false)
    expect(isGroupInUseData({ access_keys: [{ id: 7, name: '  ' }] })).toBe(false)
  })
})
