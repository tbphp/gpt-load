import type { ApiClient, ApiClientWithResponse } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  getSettings,
  projectSettings,
  settingsQueryIdentity,
  settingsResourceFromResponse,
  type SettingsDto,
} from './settings'

const settings: SettingsDto = {
  values: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: {}, remove: [] },
    inject_usage_options: true,
    request_log_retention_days: 7,
  },
  overrides: [],
}

describe('Settings resource adapter', () => {
  it('keeps locale in query identity and projects transport data before caching', async () => {
    expect(settingsQueryIdentity('zh-CN')).toEqual(controlQueryKeys.settings('zh-CN'))
    expect(settingsQueryIdentity('en-US')).not.toEqual(settingsQueryIdentity('zh-CN'))

    const requestWithResponse = vi.fn().mockResolvedValue({
      data: settings,
      status: 200,
      headers: new Headers({ ETag: `"sha256-${'c'.repeat(64)}"` }),
    })
    const client: ApiClient = {
      request: vi.fn() as ApiClient['request'],
      requestWithResponse: requestWithResponse as ApiClientWithResponse['requestWithResponse'],
    }
    await expect(getSettings(client)).resolves.toEqual({
      settings,
      settings_etag: `sha256-${'c'.repeat(64)}`,
    })
  })

  it('fails closed on unknown override scope and secret-like additions', () => {
    expect(() => projectSettings({ ...settings, overrides: ['future_setting'] })).toThrow(
      InvalidResponseError,
    )
    expect(() => projectSettings({ ...settings, auth_key: 'plaintext' })).toThrow(
      InvalidResponseError,
    )
    expect(() =>
      projectSettings({
        ...settings,
        values: { ...settings.values, secret_token: 'plaintext' },
      }),
    ).toThrow(InvalidResponseError)
  })

  it('combines the projected DTO with an unquoted opaque strong ETag', () => {
    const digest = 'a'.repeat(64)
    const resource = settingsResourceFromResponse(
      settings,
      new Headers({ ETag: `"sha256-${digest}"` }),
    )

    expect(resource).toEqual({
      settings,
      settings_etag: `sha256-${digest}`,
    })
    expect(resource.settings).not.toHaveProperty('revision')
    expect(resource.settings).not.toHaveProperty('settings_etag')
  })

  it.each([
    undefined,
    '',
    `W/"sha256-${'a'.repeat(64)}"`,
    `"sha256-${'A'.repeat(64)}"`,
    `"sha256-${'a'.repeat(63)}"`,
    `sha256-${'a'.repeat(64)}`,
    `"other-${'a'.repeat(64)}"`,
    `"sha256-${'a'.repeat(64)}", "sha256-${'b'.repeat(64)}"`,
  ])('rejects missing, weak, malformed or non-contract ETag %s', (etag) => {
    const headers = new Headers()
    if (etag !== undefined) headers.set('ETag', etag)

    expect(() => settingsResourceFromResponse(settings, headers)).toThrow(InvalidResponseError)
  })
})
