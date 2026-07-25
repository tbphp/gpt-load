import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

export type SecretSource = 'environment' | 'key_file'

export interface SecretSourceInfo {
  source: SecretSource
  path: string | null
}

export interface SystemInfoDto {
  version: string
  deployment: {
    instance_mode: 'single'
    database: 'sqlite'
    distribution: 'single_binary'
  }
  data_dir: string
  auth_key: SecretSourceInfo
  encryption: SecretSourceInfo & { enabled: true }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function projectSecretSource(value: unknown): SecretSourceInfo {
  if (
    !isRecord(value) ||
    (value.source !== 'environment' && value.source !== 'key_file') ||
    (value.path !== null && typeof value.path !== 'string')
  ) {
    throw new InvalidResponseError()
  }
  return { source: value.source, path: value.path }
}

export function projectSystemInfo(value: unknown): SystemInfoDto {
  if (
    !isRecord(value) ||
    typeof value.version !== 'string' ||
    !isRecord(value.deployment) ||
    value.deployment.instance_mode !== 'single' ||
    value.deployment.database !== 'sqlite' ||
    value.deployment.distribution !== 'single_binary' ||
    typeof value.data_dir !== 'string' ||
    !isRecord(value.encryption) ||
    value.encryption.enabled !== true
  ) {
    throw new InvalidResponseError()
  }
  const authKey = projectSecretSource(value.auth_key)
  const encryption = projectSecretSource(value.encryption)
  return {
    version: value.version,
    deployment: {
      instance_mode: 'single',
      database: 'sqlite',
      distribution: 'single_binary',
    },
    data_dir: value.data_dir,
    auth_key: authKey,
    encryption: { enabled: true, ...encryption },
  }
}

export async function getSystemInfo(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<SystemInfoDto> {
  return projectSystemInfo(await client.request<unknown>('/api/system/info', { signal }))
}
