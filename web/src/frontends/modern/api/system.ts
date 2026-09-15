import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError, NetworkError, RequestCancelledError } from '@shared/http/errors'
import { boolean, oneOf, record, text } from './response'

export const systemInfoKey = ['modern', 'system-info'] as const
export interface SecretSourceInfo {
  source: 'environment' | 'key_file'
  path: string | null
}
export interface SystemInfo {
  version: string
  database: 'sqlite' | 'mysql' | 'postgres'
  dataDir: string
  authKey: SecretSourceInfo
  encryption: SecretSourceInfo
}
function readSecretSource(value: unknown): SecretSourceInfo {
  const row = record(value)
  const source = oneOf(row.source, ['environment', 'key_file'] as const)
  const path = row.path === null ? null : asNonBlankString(row.path)
  if ((source === 'environment') !== (path === null)) throw new InvalidResponseError()
  return { source, path }
}
export async function getSystemInfo(client: ApiClient, signal: AbortSignal): Promise<SystemInfo> {
  const row = record(await client.request('/api/system/info', { signal }))
  const deployment = record(row.deployment)
  if (
    deployment.instance_mode !== 'single' ||
    deployment.distribution !== 'single_binary' ||
    !boolean(record(row.encryption).enabled)
  )
    throw new InvalidResponseError()
  return {
    version: text(row.version),
    database: oneOf(deployment.database, ['sqlite', 'mysql', 'postgres']),
    dataDir: text(row.data_dir),
    authKey: readSecretSource(row.auth_key),
    encryption: readSecretSource(row.encryption),
  }
}

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
  force: boolean,
  signal: AbortSignal,
): Promise<ReleaseUpdate | null> {
  const path = force ? '/api/system/update?force=true' : '/api/system/update'
  const data = asRecord(await client.request<unknown>(path, { signal }))
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
