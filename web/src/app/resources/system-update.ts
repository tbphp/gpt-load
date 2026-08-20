import { queryOptions } from '@tanstack/vue-query'
import type { MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectEpochMilliseconds,
  projectRecord,
  projectString,
} from './projector'

export interface ReleaseUpdateDto {
  version: string
  release_url: string
  published_at_ms: number
}

export interface SystemUpdateDto {
  update: ReleaseUpdateDto | null
}

const systemUpdateFields = ['update'] as const
const releaseUpdateFields = ['version', 'release_url', 'published_at_ms'] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankTrimmedString(value: unknown): string {
  const result = projectString(value)
  if (result !== result.trim() || result.length === 0) invalidResponse()
  return result
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

function projectReleaseUpdate(value: unknown): ReleaseUpdateDto | null {
  if (value === null) return null
  const record = projectRecord(value)
  assertExactFields(record, releaseUpdateFields)
  const version = projectNonBlankTrimmedString(record.version)
  const releaseURL = projectNonBlankTrimmedString(record.release_url)
  let parsed: URL
  try {
    parsed = new URL(releaseURL)
  } catch {
    return invalidResponse()
  }
  if (
    parsed.protocol !== 'https:' ||
    parsed.hostname !== 'github.com' ||
    parsed.port !== '' ||
    parsed.username !== '' ||
    parsed.password !== '' ||
    parsed.search !== '' ||
    parsed.hash !== '' ||
    parsed.pathname !== `/tbphp/gpt-load/releases/tag/${version}`
  ) {
    invalidResponse()
  }
  return {
    version,
    release_url: releaseURL,
    published_at_ms: projectEpochMilliseconds(record.published_at_ms),
  }
}

export function projectSystemUpdate(value: unknown): SystemUpdateDto {
  const record = projectRecord(value)
  assertExactFields(record, systemUpdateFields)
  return { update: projectReleaseUpdate(record.update) }
}

export async function getSystemUpdate(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<SystemUpdateDto> {
  return projectSystemUpdate(await client.request('/api/system/update', { signal }))
}

export function systemUpdateQueryOptions(client: ApiClient, enabled?: MaybeRefOrGetter<boolean>) {
  return queryOptions({
    queryKey: controlQueryKeys.systemUpdate(),
    queryFn: ({ signal }) => getSystemUpdate(client, signal),
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnMount: 'always',
    // 失败保持静默；只有重新进入首页才再次调用按需检查接口。
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    ...(enabled === undefined ? {} : { enabled }),
  })
}
