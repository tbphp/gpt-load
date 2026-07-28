import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import {
  assertNoSecretLikeFields,
  projectBoolean,
  projectEnum,
  projectRecord,
  projectString,
} from './projector'

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

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function assertExactFields(record: Record<string, unknown>, fields: readonly string[]): void {
  assertNoSecretLikeFields(record, fields)
  if (
    Object.keys(record).length !== fields.length ||
    fields.some((field) => !Object.prototype.hasOwnProperty.call(record, field))
  ) {
    invalidResponse()
  }
}

function projectNonBlankTrimmedString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0 || result !== result.trim()) invalidResponse()
  return result
}

function projectSecretSource(value: unknown, includeEnabled: boolean): SecretSourceInfo {
  const record = projectRecord(value)
  const fields = includeEnabled ? ['enabled', 'source', 'path'] : ['source', 'path']
  assertExactFields(record, fields)
  if (includeEnabled && projectBoolean(record.enabled) !== true) invalidResponse()
  const source = projectEnum(record.source, ['environment', 'key_file'] as const)
  const path = record.path === null ? null : projectNonBlankTrimmedString(record.path)
  if ((source === 'environment' && path !== null) || (source === 'key_file' && path === null)) {
    invalidResponse()
  }
  return { source, path }
}

export function projectSystemInfo(value: unknown): SystemInfoDto {
  const record = projectRecord(value)
  assertExactFields(record, ['version', 'deployment', 'data_dir', 'auth_key', 'encryption'])
  const deployment = projectRecord(record.deployment)
  assertExactFields(deployment, ['instance_mode', 'database', 'distribution'])
  if (
    projectEnum(deployment.instance_mode, ['single'] as const) !== 'single' ||
    projectEnum(deployment.database, ['sqlite'] as const) !== 'sqlite' ||
    projectEnum(deployment.distribution, ['single_binary'] as const) !== 'single_binary'
  ) {
    invalidResponse()
  }
  const encryption = projectSecretSource(record.encryption, true)
  return {
    version: projectNonBlankTrimmedString(record.version),
    deployment: {
      instance_mode: 'single',
      database: 'sqlite',
      distribution: 'single_binary',
    },
    data_dir: projectNonBlankTrimmedString(record.data_dir),
    auth_key: projectSecretSource(record.auth_key, false),
    encryption: { enabled: true, ...encryption },
  }
}

export async function getSystemInfo(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<SystemInfoDto> {
  return projectSystemInfo(await client.request('/api/system/info', { signal }))
}
