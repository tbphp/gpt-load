import type { ApiClient, ApiClientWithResponse } from '@/api/client'
import { ApiError, InvalidResponseError } from '@/api/errors'

import {
  getSettings,
  projectSettingsConflict,
  runtimeSettingKeys,
  updateSettings,
  type SettingsDto,
  type SettingsPatch,
} from './settings'

const response: SettingsDto = {
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
  it('uses exact GET and strong If-Match PUT contracts with response metadata', async () => {
    const signal = new AbortController().signal
    const patch: SettingsPatch = { request_timeout: 900, header_rules: null }
    const requestWithResponse = vi.fn().mockResolvedValue({
      data: response,
      status: 200,
      headers: new Headers({ ETag: `"sha256-${'a'.repeat(64)}"` }),
    })
    const client: ApiClient = {
      request: vi.fn() as ApiClient['request'],
      requestWithResponse: requestWithResponse as ApiClientWithResponse['requestWithResponse'],
    }

    await getSettings(client, signal)
    await updateSettings(client, patch, `sha256-${'a'.repeat(64)}`, signal)

    expect(requestWithResponse.mock.calls).toEqual([
      ['/api/settings', { signal }],
      [
        '/api/settings',
        {
          method: 'PUT',
          headers: { 'If-Match': `"sha256-${'a'.repeat(64)}"` },
          json: { settings: patch },
          signal,
        },
      ],
    ])
  })

  it('fails closed when the settings scope contains unknown or secret-like fields', async () => {
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
    const requestWithResponse = vi.fn().mockResolvedValue({
      data: raw,
      status: 200,
      headers: new Headers({ ETag: `"sha256-${'b'.repeat(64)}"` }),
    })

    await expect(
      getSettings({
        request: vi.fn() as ApiClient['request'],
        requestWithResponse: requestWithResponse as ApiClientWithResponse['requestWithResponse'],
      }),
    ).rejects.toBeInstanceOf(InvalidResponseError)
  })

  it('projects a strict inject_usage_options boolean', async () => {
    const requestWithResponse = vi.fn().mockResolvedValue({
      data: response,
      status: 200,
      headers: new Headers({ ETag: `"sha256-${'c'.repeat(64)}"` }),
    })
    const client: ApiClient = {
      request: vi.fn() as ApiClient['request'],
      requestWithResponse: requestWithResponse as ApiClientWithResponse['requestWithResponse'],
    }
    await expect(getSettings(client)).resolves.toMatchObject({
      settings: { values: { inject_usage_options: true } },
    })
    for (const invalid of [0, 1, 'true', null, [], {}]) {
      requestWithResponse.mockResolvedValueOnce({
        data: {
          ...response,
          values: { ...response.values, inject_usage_options: invalid },
        },
        status: 200,
        headers: new Headers({ ETag: `"sha256-${'c'.repeat(64)}"` }),
      })
      await expect(getSettings(client)).rejects.toBeInstanceOf(InvalidResponseError)
    }
  })

  it('rejects malformed settings DTOs with a generic invalid-response error', async () => {
    const requestWithResponse = vi.fn().mockResolvedValue({
      data: { ...response, overrides: 'request_timeout' },
      status: 200,
      headers: new Headers({ ETag: `"sha256-${'d'.repeat(64)}"` }),
    })

    await expect(
      getSettings({
        request: vi.fn() as ApiClient['request'],
        requestWithResponse: requestWithResponse as ApiClientWithResponse['requestWithResponse'],
      }),
    ).rejects.toBeInstanceOf(InvalidResponseError)
  })

  it('projects only a valid 412 latest Settings resource', () => {
    const token = `sha256-${'e'.repeat(64)}`
    expect(
      projectSettingsConflict(
        new ApiError(412, 'SETTINGS_VERSION_CONFLICT', 'conflict', {
          settings: response,
          settings_etag: token,
          diagnostic: 'DO_NOT_CACHE',
        }),
      ),
    ).toEqual({ settings: response, settings_etag: token })

    for (const error of [
      new ApiError(409, 'SETTINGS_VERSION_CONFLICT', 'wrong status', {
        settings: response,
        settings_etag: token,
      }),
      new ApiError(412, 'SETTINGS_VERSION_CONFLICT', 'bad token', {
        settings: response,
        settings_etag: 'weak',
      }),
      new ApiError(412, 'OTHER', 'wrong code', {
        settings: response,
        settings_etag: token,
      }),
    ]) {
      if (error.data && (error.data as { settings_etag?: string }).settings_etag === 'weak') {
        expect(() => projectSettingsConflict(error)).toThrow(InvalidResponseError)
      } else {
        expect(projectSettingsConflict(error)).toBeUndefined()
      }
    }
  })
})
