import { InvalidResponseError } from '@/api/errors'
import type { SettingsDto } from '@/api/control/settings'

import { settingsResourceFromResponse } from './settings'

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
