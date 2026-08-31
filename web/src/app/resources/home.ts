import { queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import type {
  AccessKeyCollectionItemDto,
  AccessProtocol,
  CredentialItemDto,
} from '@/api/control/types'
import { knownAccessProtocols } from '@/api/control/protocols'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import { projectAccessKeyCollectionItem } from './access-keys'
import type { ChannelCapabilitiesDto } from './channels'
import { projectCredentialItem } from './credentials'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectEpochMilliseconds,
  projectNonNegativeInt64String,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type HomeRange = '24h' | '30d'
export type HomeStatisticsGranularity = 'hour' | 'day'

export interface HomeBaseDto {
  server_now_ms: number
  started_at_ms: number
  version: string
  inventory: {
    group_count: number
    credential_count: number
    available_credential_count: number
    model_count: number
  }
  access_keys: Array<{
    id: number
    name: string
    masked_key: string
    protocols: AccessProtocol[]
  }>
  current_access_key: AccessKeyCollectionItemDto | null
}

export interface HomeStatisticsSummary {
  request_count: number
  success_count: number
  failure_count: number
  total_tokens: number
  input_tokens: number
  cache_read_tokens: number
  cache_write_unknown_tokens: number
  estimated_cost_nano_usd: string
  usage_missing_count: number
  partial_count: number
  unpriced_request_count: number
  pricing_partial_count: number
}

export interface HomeTrendPoint {
  bucket_start_ms: number
  bucket_end_ms: number
  request_count: number
  failure_count: number
}

export interface HomeStatisticsRef {
  id: number
  name: string | null
  deleted: boolean
}

export interface HomeModelRanking {
  model: string
  request_count: number
  total_tokens: number
  estimated_cost_nano_usd: string
}

export interface HomeGroupRanking {
  group: HomeStatisticsRef
  request_count: number
  total_tokens: number
  estimated_cost_nano_usd: string
}

export interface HomeAccessKeyRanking {
  access_key: HomeStatisticsRef
  request_count: number
  total_tokens: number
  estimated_cost_nano_usd: string
}

export interface HomeRankings {
  models: HomeModelRanking[]
  groups: HomeGroupRanking[]
  access_keys: HomeAccessKeyRanking[]
}

export interface HomeStatisticsDto {
  range: HomeRange
  granularity: HomeStatisticsGranularity
  from_ms: number
  to_ms: number
  observed_at_ms: number
  summary: HomeStatisticsSummary
  series: HomeTrendPoint[]
  rankings: HomeRankings
}

export interface HomeSubscriptionAccountDto {
  channel_id: string
  channel_mark: string
  channel_icon: string
  capabilities: ChannelCapabilitiesDto
  group_count: number
  available_group_count: number
  credential: CredentialItemDto
}

export interface HomeSubscriptionAccountsDto {
  observed_at_ms: number
  items: HomeSubscriptionAccountDto[]
}

const homeBaseFields = [
  'server_now_ms',
  'started_at_ms',
  'version',
  'inventory',
  'access_keys',
  'current_access_key',
] as const
const inventoryFields = [
  'group_count',
  'credential_count',
  'available_credential_count',
  'model_count',
] as const
const accessKeyFields = ['id', 'name', 'masked_key', 'protocols'] as const
const statisticsFields = [
  'range',
  'granularity',
  'from_ms',
  'to_ms',
  'observed_at_ms',
  'summary',
  'series',
  'rankings',
] as const
const summaryFields = [
  'request_count',
  'success_count',
  'failure_count',
  'total_tokens',
  'input_tokens',
  'cache_read_tokens',
  'cache_write_unknown_tokens',
  'estimated_cost_nano_usd',
  'usage_missing_count',
  'partial_count',
  'unpriced_request_count',
  'pricing_partial_count',
] as const
const trendPointFields = [
  'bucket_start_ms',
  'bucket_end_ms',
  'request_count',
  'failure_count',
] as const
const rankingsFields = ['models', 'groups', 'access_keys'] as const
const statisticsRefFields = ['id', 'name', 'deleted'] as const
const rankingMetricFields = ['request_count', 'total_tokens', 'estimated_cost_nano_usd'] as const
const modelRankingFields = ['model', ...rankingMetricFields] as const
const groupRankingFields = ['group', ...rankingMetricFields] as const
const accessKeyRankingFields = ['access_key', ...rankingMetricFields] as const
const subscriptionAccountsFields = ['observed_at_ms', 'items'] as const
const subscriptionAccountFields = [
  'channel_id',
  'channel_mark',
  'channel_icon',
  'capabilities',
  'group_count',
  'available_group_count',
  'credential',
] as const
const subscriptionCapabilitiesFields = [
  'model_discovery',
  'quota_observation',
  'credential_actions',
  'outbound_proxy',
] as const
const hourMilliseconds = 3_600_000
const dayMilliseconds = 86_400_000
const rankingLimit = 5
const textEncoder = new TextEncoder()

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankTrimmedString(value: unknown): string {
  const result = projectString(value)
  if (result !== result.trim() || result.length === 0) invalidResponse()
  return result
}

function projectHomeInventory(value: unknown): HomeBaseDto['inventory'] {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, inventoryFields)
  const result = {
    group_count: projectSafeInteger(record.group_count, { minimum: 0 }),
    credential_count: projectSafeInteger(record.credential_count, { minimum: 0 }),
    available_credential_count: projectSafeInteger(record.available_credential_count, {
      minimum: 0,
    }),
    model_count: projectSafeInteger(record.model_count, { minimum: 0 }),
  }
  if (result.available_credential_count > result.credential_count) invalidResponse()
  return result
}

function projectHomeAccessKey(value: unknown): HomeBaseDto['access_keys'][number] {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, accessKeyFields)
  const maskedKey = projectString(record.masked_key)
  const protocols = projectArray(record.protocols, (protocol) =>
    projectEnum(protocol, knownAccessProtocols),
  )
  if (new Set(protocols).size !== protocols.length) invalidResponse()
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankTrimmedString(record.name),
    masked_key: maskedKey,
    protocols,
  }
}

export function projectHomeBase(value: unknown): HomeBaseDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, homeBaseFields)
  const serverNowMS = projectEpochMilliseconds(record.server_now_ms)
  const startedAtMS = projectEpochMilliseconds(record.started_at_ms)
  if (startedAtMS > serverNowMS) invalidResponse()
  const accessKeys = projectArray(record.access_keys, projectHomeAccessKey)
  let previousID = 0
  for (const accessKey of accessKeys) {
    if (accessKey.id <= previousID) invalidResponse()
    previousID = accessKey.id
  }
  return {
    server_now_ms: serverNowMS,
    started_at_ms: startedAtMS,
    version: projectNonBlankTrimmedString(record.version),
    inventory: projectHomeInventory(record.inventory),
    access_keys: accessKeys,
    current_access_key:
      record.current_access_key === null
        ? null
        : projectAccessKeyCollectionItem(record.current_access_key),
  }
}

function projectHomeSubscriptionCapabilities(value: unknown): ChannelCapabilitiesDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, subscriptionCapabilitiesFields)
  const actions = projectArray(record.credential_actions, (action) =>
    projectEnum(action, ['reset_credit'] as const),
  )
  if (new Set(actions).size !== actions.length) invalidResponse()
  return {
    model_discovery: projectBoolean(record.model_discovery),
    quota_observation: projectBoolean(record.quota_observation),
    credential_actions: actions,
    outbound_proxy: projectBoolean(record.outbound_proxy),
  }
}

function projectHomeSubscriptionAccount(value: unknown): HomeSubscriptionAccountDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, subscriptionAccountFields)
  const groupCount = projectSafeInteger(record.group_count, { minimum: 1 })
  const availableGroupCount = projectSafeInteger(record.available_group_count, { minimum: 0 })
  const credential = projectCredentialItem(record.credential)
  if (availableGroupCount > groupCount || credential.connection_type !== 'subscription') {
    invalidResponse()
  }
  return {
    channel_id: projectNonBlankTrimmedString(record.channel_id),
    channel_mark: projectNonBlankTrimmedString(record.channel_mark),
    channel_icon: projectNonBlankTrimmedString(record.channel_icon),
    capabilities: projectHomeSubscriptionCapabilities(record.capabilities),
    group_count: groupCount,
    available_group_count: availableGroupCount,
    credential,
  }
}

export function projectHomeSubscriptionAccounts(value: unknown): HomeSubscriptionAccountsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, subscriptionAccountsFields)
  const items = projectArray(record.items, projectHomeSubscriptionAccount)
  if (
    items.length > 4 ||
    new Set(items.map((item) => item.credential.credential_id)).size !== items.length
  ) {
    invalidResponse()
  }
  return {
    observed_at_ms: projectEpochMilliseconds(record.observed_at_ms),
    items,
  }
}

function projectHomeStatisticsSummary(value: unknown): HomeStatisticsSummary {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, summaryFields)
  const result: HomeStatisticsSummary = {
    request_count: projectSafeInteger(record.request_count, { minimum: 0 }),
    success_count: projectSafeInteger(record.success_count, { minimum: 0 }),
    failure_count: projectSafeInteger(record.failure_count, { minimum: 0 }),
    total_tokens: projectSafeInteger(record.total_tokens, { minimum: 0 }),
    input_tokens: projectSafeInteger(record.input_tokens, { minimum: 0 }),
    cache_read_tokens: projectSafeInteger(record.cache_read_tokens, { minimum: 0 }),
    cache_write_unknown_tokens: projectSafeInteger(record.cache_write_unknown_tokens, {
      minimum: 0,
    }),
    estimated_cost_nano_usd: projectNonNegativeInt64String(record.estimated_cost_nano_usd),
    usage_missing_count: projectSafeInteger(record.usage_missing_count, { minimum: 0 }),
    partial_count: projectSafeInteger(record.partial_count, { minimum: 0 }),
    unpriced_request_count: projectSafeInteger(record.unpriced_request_count, {
      minimum: 0,
    }),
    pricing_partial_count: projectSafeInteger(record.pricing_partial_count, { minimum: 0 }),
  }
  if (
    result.success_count > result.request_count ||
    result.failure_count > result.request_count ||
    result.success_count + result.failure_count !== result.request_count ||
    result.cache_read_tokens > result.input_tokens ||
    result.input_tokens > result.total_tokens ||
    result.usage_missing_count > result.request_count ||
    result.partial_count > result.request_count ||
    result.unpriced_request_count > result.request_count ||
    result.pricing_partial_count > result.request_count
  ) {
    invalidResponse()
  }
  return result
}

function projectStatisticsRef(value: unknown): HomeStatisticsRef {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, statisticsRefFields)
  const id = projectSafeInteger(record.id, { minimum: 0 })
  const deleted = projectBoolean(record.deleted)
  const name = record.name === null ? null : projectNonBlankTrimmedString(record.name)
  if (deleted !== (name === null) || (id === 0 && !deleted)) invalidResponse()
  return { id, name, deleted }
}

function projectRankingMetrics(
  record: Record<string, unknown>,
): Pick<HomeModelRanking, 'request_count' | 'total_tokens' | 'estimated_cost_nano_usd'> {
  return {
    request_count: projectSafeInteger(record.request_count, { minimum: 0 }),
    total_tokens: projectSafeInteger(record.total_tokens, { minimum: 0 }),
    estimated_cost_nano_usd: projectNonNegativeInt64String(record.estimated_cost_nano_usd),
  }
}

function projectModelRanking(value: unknown): HomeModelRanking {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, modelRankingFields)
  const model = projectString(record.model, { allowEmpty: true })
  if (model !== model.trim()) invalidResponse()
  return {
    model,
    ...projectRankingMetrics(record),
  }
}

function projectGroupRanking(value: unknown): HomeGroupRanking {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupRankingFields)
  return {
    group: projectStatisticsRef(record.group),
    ...projectRankingMetrics(record),
  }
}

function projectAccessKeyRanking(value: unknown): HomeAccessKeyRanking {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, accessKeyRankingFields)
  return {
    access_key: projectStatisticsRef(record.access_key),
    ...projectRankingMetrics(record),
  }
}

interface RankingMetrics {
  request_count: number
  total_tokens: number
  estimated_cost_nano_usd: string
}

function compareUTF8(left: string, right: string): number {
  const leftBytes = textEncoder.encode(left)
  const rightBytes = textEncoder.encode(right)
  const length = Math.min(leftBytes.length, rightBytes.length)
  for (let index = 0; index < length; index += 1) {
    const difference = leftBytes[index]! - rightBytes[index]!
    if (difference !== 0) return difference
  }
  return leftBytes.length - rightBytes.length
}

function assertRankingOrder<T extends RankingMetrics>(
  rows: T[],
  compareIdentity: (left: T, right: T) => number,
  identity: (row: T) => string,
  summary: HomeStatisticsSummary,
): void {
  if (rows.length > rankingLimit) invalidResponse()
  const identities = new Set<string>()
  const summaryCost = BigInt(summary.estimated_cost_nano_usd)
  for (let index = 0; index < rows.length; index += 1) {
    const row = rows[index]!
    const rowIdentity = identity(row)
    if (
      identities.has(rowIdentity) ||
      row.request_count > summary.request_count ||
      row.total_tokens > summary.total_tokens ||
      BigInt(row.estimated_cost_nano_usd) > summaryCost
    ) {
      invalidResponse()
    }
    identities.add(rowIdentity)

    if (index === 0) continue
    const previous = rows[index - 1]!
    const previousCost = BigInt(previous.estimated_cost_nano_usd)
    const rowCost = BigInt(row.estimated_cost_nano_usd)
    if (
      rowCost > previousCost ||
      (rowCost === previousCost && row.request_count > previous.request_count) ||
      (rowCost === previousCost &&
        row.request_count === previous.request_count &&
        compareIdentity(previous, row) >= 0)
    ) {
      invalidResponse()
    }
  }
}

function addSafeInteger(left: number, right: number): number {
  const result = left + right
  if (!Number.isSafeInteger(result)) invalidResponse()
  return result
}

function rangeContract(range: HomeRange): {
  granularity: HomeStatisticsGranularity
  bucketMilliseconds: number
  bucketCount: number
} {
  return range === '24h'
    ? { granularity: 'hour', bucketMilliseconds: hourMilliseconds, bucketCount: 24 }
    : { granularity: 'day', bucketMilliseconds: dayMilliseconds, bucketCount: 30 }
}

export function projectHomeStatistics(
  value: unknown,
  expectedRange?: HomeRange,
): HomeStatisticsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, statisticsFields)
  const range = projectEnum(record.range, ['24h', '30d'] as const)
  if (expectedRange !== undefined && range !== expectedRange) invalidResponse()
  const contract = rangeContract(range)
  const granularity = projectEnum(record.granularity, ['hour', 'day'] as const)
  if (granularity !== contract.granularity) invalidResponse()

  const fromMS = projectEpochMilliseconds(record.from_ms)
  const toMS = projectEpochMilliseconds(record.to_ms)
  const observedAtMS = projectEpochMilliseconds(record.observed_at_ms)
  if (
    fromMS % contract.bucketMilliseconds !== 0 ||
    toMS % contract.bucketMilliseconds !== 0 ||
    toMS - fromMS !== contract.bucketMilliseconds * contract.bucketCount ||
    observedAtMS < toMS - contract.bucketMilliseconds ||
    observedAtMS >= toMS
  ) {
    invalidResponse()
  }

  const summary = projectHomeStatisticsSummary(record.summary)
  let requestCount = 0
  let failureCount = 0
  let seriesIndex = 0
  const series = projectArray(record.series, (value): HomeTrendPoint => {
    const point = projectRecord(value)
    assertNoSecretLikeFields(point, trendPointFields)
    const bucketStartMS = projectEpochMilliseconds(point.bucket_start_ms)
    const bucketEndMS = projectEpochMilliseconds(point.bucket_end_ms)
    const pointRequestCount = projectSafeInteger(point.request_count, { minimum: 0 })
    const pointFailureCount = projectSafeInteger(point.failure_count, { minimum: 0 })
    if (
      bucketStartMS !== fromMS + seriesIndex * contract.bucketMilliseconds ||
      bucketEndMS !== bucketStartMS + contract.bucketMilliseconds ||
      pointFailureCount > pointRequestCount
    ) {
      invalidResponse()
    }
    seriesIndex += 1
    requestCount = addSafeInteger(requestCount, pointRequestCount)
    failureCount = addSafeInteger(failureCount, pointFailureCount)
    return {
      bucket_start_ms: bucketStartMS,
      bucket_end_ms: bucketEndMS,
      request_count: pointRequestCount,
      failure_count: pointFailureCount,
    }
  })
  if (
    series.length !== contract.bucketCount ||
    requestCount !== summary.request_count ||
    failureCount !== summary.failure_count
  ) {
    invalidResponse()
  }

  const rankingsRecord = projectRecord(record.rankings)
  assertNoSecretLikeFields(rankingsRecord, rankingsFields)
  const rankings: HomeRankings = {
    models: projectArray(rankingsRecord.models, projectModelRanking),
    groups: projectArray(rankingsRecord.groups, projectGroupRanking),
    access_keys: projectArray(rankingsRecord.access_keys, projectAccessKeyRanking),
  }
  assertRankingOrder(
    rankings.models,
    (left, right) => compareUTF8(left.model, right.model),
    (row) => row.model,
    summary,
  )
  assertRankingOrder(
    rankings.groups,
    (left, right) => left.group.id - right.group.id,
    (row) => String(row.group.id),
    summary,
  )
  assertRankingOrder(
    rankings.access_keys,
    (left, right) => left.access_key.id - right.access_key.id,
    (row) => String(row.access_key.id),
    summary,
  )

  return {
    range,
    granularity,
    from_ms: fromMS,
    to_ms: toMS,
    observed_at_ms: observedAtMS,
    summary,
    series,
    rankings,
  }
}

export function createEmptyHomeStatistics(
  range: HomeRange,
  observedAtMS: number = Date.now(),
): HomeStatisticsDto {
  const observed = projectEpochMilliseconds(observedAtMS)
  const contract = rangeContract(range)
  const currentBucketStart = observed - (observed % contract.bucketMilliseconds)
  const toMS = currentBucketStart + contract.bucketMilliseconds
  const fromMS = toMS - contract.bucketMilliseconds * contract.bucketCount
  if (!Number.isSafeInteger(toMS) || fromMS < 0) invalidResponse()
  return {
    range,
    granularity: contract.granularity,
    from_ms: fromMS,
    to_ms: toMS,
    observed_at_ms: observed,
    summary: {
      request_count: 0,
      success_count: 0,
      failure_count: 0,
      total_tokens: 0,
      input_tokens: 0,
      cache_read_tokens: 0,
      cache_write_unknown_tokens: 0,
      estimated_cost_nano_usd: '0',
      usage_missing_count: 0,
      partial_count: 0,
      unpriced_request_count: 0,
      pricing_partial_count: 0,
    },
    series: Array.from({ length: contract.bucketCount }, (_, index) => {
      const bucketStartMS = fromMS + index * contract.bucketMilliseconds
      return {
        bucket_start_ms: bucketStartMS,
        bucket_end_ms: bucketStartMS + contract.bucketMilliseconds,
        request_count: 0,
        failure_count: 0,
      }
    }),
    rankings: { models: [], groups: [], access_keys: [] },
  }
}

export async function getHomeBase(client: ApiClient, signal?: AbortSignal): Promise<HomeBaseDto> {
  return projectHomeBase(await client.request('/api/home', { method: 'GET', signal }))
}

export async function getHomeStatistics(
  client: ApiClient,
  range: HomeRange,
  signal?: AbortSignal,
): Promise<HomeStatisticsDto> {
  const params = new URLSearchParams([['range', range]])
  return projectHomeStatistics(
    await client.request(`/api/home/statistics?${params.toString()}`, {
      method: 'GET',
      signal,
    }),
    range,
  )
}

export async function getHomeSubscriptionAccounts(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<HomeSubscriptionAccountsDto> {
  return projectHomeSubscriptionAccounts(
    await client.request('/api/home/subscription-accounts', { method: 'GET', signal }),
  )
}

export function homeBaseQueryOptions(client: ApiClient) {
  return queryOptions({
    queryKey: controlQueryKeys.home.base(),
    queryFn: ({ signal }) => getHomeBase(client, signal),
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnMount: 'always',
  })
}

export function homeSubscriptionAccountsQueryOptions(
  client: ApiClient,
  enabled: MaybeRefOrGetter<boolean>,
) {
  return queryOptions({
    queryKey: controlQueryKeys.home.subscriptionAccounts(),
    queryFn: ({ signal }) => getHomeSubscriptionAccounts(client, signal),
    enabled: computed(() => toValue(enabled)),
    staleTime: 60_000,
    refetchOnMount: 'always',
  })
}

export function homeStatisticsQueryOptions(client: ApiClient, range: MaybeRefOrGetter<HomeRange>) {
  return queryOptions({
    queryKey: computed(() => controlQueryKeys.home.statistics(toValue(range))),
    queryFn: ({ queryKey, signal }) => {
      const queryRange = projectEnum(queryKey[3], ['24h', '30d'] as const)
      return getHomeStatistics(client, queryRange, signal)
    },
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnMount: 'always',
  })
}
