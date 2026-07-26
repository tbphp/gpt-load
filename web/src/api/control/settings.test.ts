import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import {
  getSettings,
  runtimeSettingKeys,
  updateSettings,
  type SettingsDto,
  type SettingsPatch,
} from './settings'

const response: SettingsDto = {
  revision: 7,
  values: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Test': 'HEADER_CANARY' }, remove: ['X-Debug'] },
    inject_usage_options: true,
    request_log_retention_days: 7,
  },
  overrides: ['request_timeout', 'header_rules'],
}

describe('Settings control API', () => {
  it('uses exact GET and PUT contracts with AbortSignals and a settings patch body', async () => {
    const signal = new AbortController().signal
    const patch: SettingsPatch = { request_timeout: 900, header_rules: null }
    const request = vi.fn().mockResolvedValue(response)
    const client: ApiClient = { request: request as ApiClient['request'] }

    await getSettings(client, signal)
    await updateSettings(client, patch, signal)

    expect(request.mock.calls).toEqual([
      ['/api/settings', { signal }],
      ['/api/settings', { method: 'PUT', json: { settings: patch }, signal }],
    ])
  })

  it('projects only the exact runtime settings and keeps overrides as a string array', async () => {
    expect(runtimeSettingKeys).toEqual([
      'connect_timeout',
      'first_byte_timeout',
      'request_timeout',
      'stream_idle_timeout',
      'header_rules',
      'inject_usage_options',
      'request_log_retention_days',
    ])
    const raw = {
      ...response,
      values: { ...response.values, auth_key: 'DO_NOT_CACHE', future_setting: true },
      overrides: [...response.overrides, 'future_setting'],
      secret_debug: 'DO_NOT_CACHE',
    }
    const request = vi.fn().mockResolvedValue(raw)

    const settings = await getSettings({ request: request as ApiClient['request'] })

    expect(settings).toEqual(response)
    expect(Array.isArray(settings.overrides)).toBe(true)
    expect(JSON.stringify(settings)).not.toContain('DO_NOT_CACHE')
    expect(Object.keys(settings.values)).toEqual(runtimeSettingKeys)
  })

  it('projects a strict inject_usage_options boolean', async () => {
    const request = vi.fn().mockResolvedValue(response)
    await expect(getSettings({ request: request as ApiClient['request'] })).resolves.toMatchObject({
      values: { inject_usage_options: true },
    })
    for (const invalid of [0, 1, 'true', null, [], {}]) {
      request.mockResolvedValueOnce({
        ...response,
        values: { ...response.values, inject_usage_options: invalid },
      })
      await expect(
        getSettings({ request: request as ApiClient['request'] }),
      ).rejects.toBeInstanceOf(InvalidResponseError)
    }
  })

  it('rejects malformed settings DTOs with a generic invalid-response error', async () => {
    const request = vi.fn().mockResolvedValue({ ...response, overrides: 'request_timeout' })

    await expect(getSettings({ request: request as ApiClient['request'] })).rejects.toBeInstanceOf(
      InvalidResponseError,
    )
  })
})
