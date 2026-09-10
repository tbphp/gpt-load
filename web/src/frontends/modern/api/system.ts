import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError, NetworkError, RequestCancelledError } from '@shared/http/errors'

export interface ReleaseUpdate {
  version: string
  releaseURL: string
}

function asRecord(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new InvalidResponseError()
  }
  return value as Record<string, unknown>
}

function asNonBlankString(value: unknown): string {
  if (typeof value !== 'string' || !value.length || value.trim() !== value) {
    throw new InvalidResponseError()
  }
  return value
}

export async function getCurrentVersion(signal: AbortSignal): Promise<string> {
  let response: Response
  try {
    response = await fetch('/health', {
      signal,
      cache: 'no-store',
      headers: { Accept: 'application/json' },
    })
  } catch {
    if (signal.aborted) throw new RequestCancelledError()
    throw new NetworkError()
  }
  if (!response.ok) throw new NetworkError()
  let data: unknown
  try {
    data = await response.json()
  } catch {
    if (signal.aborted) throw new RequestCancelledError()
    throw new InvalidResponseError()
  }
  const record = asRecord(data)
  if (record.status !== 'ok') throw new InvalidResponseError()
  return asNonBlankString(record.version)
}

export async function getReleaseUpdate(
  client: ApiClient,
  authKey: string,
  force: boolean,
  signal: AbortSignal,
): Promise<ReleaseUpdate | null> {
  const path = force ? '/api/system/update?force=true' : '/api/system/update'
  const data = asRecord(await client.request<unknown>(path, { authKey, signal }))
  if (data.update === null) return null
  const update = asRecord(data.update)
  const version = asNonBlankString(update.version)
  const releaseURL = asNonBlankString(update.release_url)
  let url: URL
  try {
    url = new URL(releaseURL)
  } catch {
    throw new InvalidResponseError()
  }
  if (
    url.protocol !== 'https:' ||
    url.hostname !== 'github.com' ||
    url.port ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== `/tbphp/gpt-load/releases/tag/${version}`
  ) {
    throw new InvalidResponseError()
  }
  return { version, releaseURL }
}
