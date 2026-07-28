import type { SettingsDto } from '@/api/control/settings'
import { InvalidResponseError } from '@/api/errors'

export interface SettingsResource {
  settings: SettingsDto
  settings_etag: string
}

const strongSettingsETag = /^"(?<token>sha256-[0-9a-f]{64})"$/
const strongSettingsETagToken = /^sha256-[0-9a-f]{64}$/

export function settingsResourceFromToken(settings: SettingsDto, token: string): SettingsResource {
  if (!strongSettingsETagToken.test(token)) throw new InvalidResponseError()
  return {
    settings,
    settings_etag: token,
  }
}

export function settingsResourceFromResponse(
  settings: SettingsDto,
  headers: Headers,
): SettingsResource {
  const header = headers.get('ETag')
  const match = header?.match(strongSettingsETag)
  const token = match?.groups?.token
  if (!token) throw new InvalidResponseError()

  return settingsResourceFromToken(settings, token)
}
