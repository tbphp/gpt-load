import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import { createGroup, listGroups, projectGroupDetail, projectGroupList } from './groups'

const group = {
  id: 7,
  name: 'Primary',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai', 'anthropic'],
  models: [{ id: 'gpt-4o', alias: '' }],
  enabled: true,
  key_count: 2,
}

const detail = {
  ...group,
  validation_model: 'gpt-4o',
  weight_manual: 50,
  config: {
    connect_timeout: 20,
    header_rules: { set: { Authorization: 'secret-bearing-config-is-allowed' }, remove: [] },
  },
  effective_config: {
    connect_timeout: 20,
    first_byte_timeout: 60,
    request_timeout: 900,
    stream_idle_timeout: 120,
    header_rules: { set: {}, remove: ['X-Legacy'] },
    inject_usage_options: false,
  },
}

describe('Group resource', () => {
  it('projects complete list and detail DTOs', () => {
    expect(projectGroupList([group])).toEqual([group])
    expect(projectGroupDetail(detail)).toEqual(detail)
  })

  it.each([
    [{ ...group, id: Number.MAX_SAFE_INTEGER + 1 }],
    [{ ...group, upstream_url: 'file:///tmp/upstream' }],
    [{ ...group, protocols: ['unknown'] }],
    [{ ...group, key_count: Number.POSITIVE_INFINITY }],
    [{ ...group, models: [{ id: '', alias: '' }] }],
    [{ ...group, upstream_secret_token: 'plaintext' }],
  ])('rejects an unsafe Group list entry %#j', (unsafe) => {
    expect(() => projectGroupList(unsafe)).toThrow(InvalidResponseError)
  })

  it('rejects malformed detail-only fields and secret-like additions', () => {
    expect(() => projectGroupDetail({ ...detail, weight_manual: 101 })).toThrow(
      InvalidResponseError,
    )
    expect(() =>
      projectGroupDetail({
        ...detail,
        effective_config: { ...detail.effective_config, request_timeout: Number.NaN },
      }),
    ).toThrow(InvalidResponseError)
    expect(() => projectGroupDetail({ ...detail, raw_key: 'plaintext' })).toThrow(
      InvalidResponseError,
    )
  })

  it('projects transport data before returning', async () => {
    const request = vi.fn().mockResolvedValue([group])
    const apiRequest = request as ApiClient['request']
    await expect(listGroups({ request: apiRequest })).resolves.toEqual([group])
    request.mockResolvedValueOnce([{ ...group, id: 0 }])
    await expect(listGroups({ request: apiRequest })).rejects.toBeInstanceOf(InvalidResponseError)
  })

  it('serializes an idempotent create and projects its non-secret result', async () => {
    const response = {
      group_id: 7,
      group_name: 'Primary',
      keys_added: 1,
      keys_duplicated: 0,
      models: [{ id: 'gpt-4o', alias: '' }],
    }
    const request = vi.fn().mockResolvedValue(response) as ApiClient['request']
    const body = {
      upstream_url: 'https://api.example.com',
      protocols: ['openai'] as const,
      models: response.models,
      config: {},
      keys: 'raw-key',
      confirm_same_upstream_url: false,
    }

    await expect(createGroup({ request }, body, 'operation-id')).resolves.toEqual(response)
    expect(request).toHaveBeenCalledWith('/api/groups', {
      method: 'POST',
      headers: { 'Idempotency-Key': 'operation-id' },
      json: body,
      signal: undefined,
    })
  })
})
