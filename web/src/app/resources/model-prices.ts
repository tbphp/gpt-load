import { keepPreviousData, queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { ApiError, InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectEpochMilliseconds,
  projectNullableDecimalString,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type ModelPriceStatus = 'pending' | 'configured'
export type ModelPriceStatusFilter = ModelPriceStatus | 'all'
export type ModelPriceUsageFilter = 'in_use' | 'unreferenced' | 'all'
export type ModelPriceMethod = 'auto_sync' | 'user_override' | 'user_set' | 'user_marked_unpriced'

export interface ModelPriceFilters {
  usage: ModelPriceUsageFilter
  status: ModelPriceStatusFilter
  search?: string
  page: number
  page_size: 20 | 50 | 100
}

export interface ModelPriceScopeDto {
  kind: 'provider' | 'group'
  id: string
  label: string
}

export interface ModelPriceSlotsDto {
  input: string | null
  output: string | null
  cache_read: string | null
  cache_write: string | null
}

export interface ModelPriceDto {
  id: number
  model_id: string
  scope: ModelPriceScopeDto
  prices: ModelPriceSlotsDto
  pricing_status: ModelPriceStatus
  method: ModelPriceMethod | null
  referenced: boolean
  reference_count: number
  reference_group_count: number
  has_context_tiers: boolean
  partial: boolean
  updated_at_ms: number
  can_reset: boolean
  can_delete: boolean
}

export interface ModelPricePaginationDto {
  page: number
  page_size: 20 | 50 | 100
  total_items: number
  total_pages: number
}

export interface ModelPriceCollectionDto {
  items: ModelPriceDto[]
  pagination: ModelPricePaginationDto
}

export interface ModelPriceUpdateRequest {
  input: string | null
  output: string | null
  cache_read: string | null
  cache_write: string | null
  confirm_unpriced: boolean
}

export type ModelPriceMutationIssue =
  | { code: 'MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED'; id: number }
  | {
      code: 'MODEL_PRICE_REFERENCED'
      id: number
      reference_count: number
      reference_group_count: number
    }
  | { code: 'MODEL_PRICE_AUTOMATIC_DELETE_FORBIDDEN'; id: number }

const collectionFields = ['items', 'pagination'] as const
const paginationFields = ['page', 'page_size', 'total_items', 'total_pages'] as const
const itemFields = [
  'id',
  'model_id',
  'scope',
  'prices',
  'pricing_status',
  'method',
  'referenced',
  'reference_count',
  'reference_group_count',
  'has_context_tiers',
  'partial',
  'updated_at_ms',
  'can_reset',
  'can_delete',
] as const
const scopeFields = ['kind', 'id', 'label'] as const
const priceFields = ['input', 'output', 'cache_read', 'cache_write'] as const
const updateFields = [...priceFields, 'confirm_unpriced'] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectIdentityString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0 || /\p{Cc}/u.test(result)) invalidResponse()
  return result
}

function projectScope(value: unknown): ModelPriceScopeDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, scopeFields)
  return {
    kind: projectEnum(record.kind, ['provider', 'group'] as const),
    id: projectIdentityString(record.id),
    label: projectIdentityString(record.label),
  }
}

function projectPrices(value: unknown): ModelPriceSlotsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, priceFields)
  return {
    input: projectNullableDecimalString(record.input),
    output: projectNullableDecimalString(record.output),
    cache_read: projectNullableDecimalString(record.cache_read),
    cache_write: projectNullableDecimalString(record.cache_write),
  }
}

function projectMethod(value: unknown): ModelPriceMethod | null {
  return value === null
    ? null
    : projectEnum(value, [
        'auto_sync',
        'user_override',
        'user_set',
        'user_marked_unpriced',
      ] as const)
}

export function projectModelPrice(value: unknown): ModelPriceDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, itemFields)
  const result: ModelPriceDto = {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    model_id: projectIdentityString(record.model_id),
    scope: projectScope(record.scope),
    prices: projectPrices(record.prices),
    pricing_status: projectEnum(record.pricing_status, ['pending', 'configured'] as const),
    method: projectMethod(record.method),
    referenced: projectBoolean(record.referenced),
    reference_count: projectSafeInteger(record.reference_count, { minimum: 0 }),
    reference_group_count: projectSafeInteger(record.reference_group_count, { minimum: 0 }),
    has_context_tiers: projectBoolean(record.has_context_tiers),
    partial: projectBoolean(record.partial),
    updated_at_ms: projectEpochMilliseconds(record.updated_at_ms),
    can_reset: projectBoolean(record.can_reset),
    can_delete: projectBoolean(record.can_delete),
  }
  if (
    result.reference_group_count > result.reference_count ||
    result.referenced !== result.reference_count > 0 ||
    (result.pricing_status === 'pending' && result.method !== null) ||
    (result.can_delete && (!result.can_reset || result.referenced))
  ) {
    invalidResponse()
  }
  return result
}

function projectPagination(value: unknown): ModelPricePaginationDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, paginationFields)
  const pageSize = projectSafeInteger(record.page_size, { minimum: 1, maximum: 100 })
  if (pageSize !== 20 && pageSize !== 50 && pageSize !== 100) invalidResponse()
  return {
    page: projectSafeInteger(record.page, { minimum: 1 }),
    page_size: pageSize,
    total_items: projectSafeInteger(record.total_items, { minimum: 0 }),
    total_pages: projectSafeInteger(record.total_pages, { minimum: 0 }),
  }
}

function expectedPageItems(pagination: ModelPricePaginationDto): number {
  if (pagination.total_items === 0 || pagination.page > pagination.total_pages) return 0
  if (pagination.page < pagination.total_pages) return pagination.page_size
  const remainder = pagination.total_items % pagination.page_size
  return remainder === 0 ? pagination.page_size : remainder
}

export function projectModelPriceCollection(value: unknown): ModelPriceCollectionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, collectionFields)
  const items = projectArray(record.items, projectModelPrice)
  const pagination = projectPagination(record.pagination)
  const expectedTotalPages =
    pagination.total_items === 0 ? 0 : Math.ceil(pagination.total_items / pagination.page_size)
  if (
    pagination.total_pages !== expectedTotalPages ||
    items.length !== expectedPageItems(pagination) ||
    new Set(items.map(({ id }) => id)).size !== items.length
  ) {
    invalidResponse()
  }
  return { items, pagination }
}

export function normalizeModelPriceFilters(filters: ModelPriceFilters): ModelPriceFilters {
  const normalized: ModelPriceFilters = {
    usage: filters.usage,
    status: filters.status,
    page: filters.page,
    page_size: filters.page_size,
  }
  const search = filters.search?.trim()
  if (search) normalized.search = [...search].slice(0, 200).join('')
  return normalized
}

export async function listModelPrices(
  client: ApiClient,
  filters: ModelPriceFilters,
  signal?: AbortSignal,
): Promise<ModelPriceCollectionDto> {
  const normalized = normalizeModelPriceFilters(filters)
  const params = new URLSearchParams({
    usage: normalized.usage,
    status: normalized.status,
    page: String(normalized.page),
    page_size: String(normalized.page_size),
  })
  if (normalized.search) params.set('search', normalized.search)
  const result = projectModelPriceCollection(
    await client.request(`/api/model-prices?${params.toString()}`, { method: 'GET', signal }),
  )
  if (
    result.pagination.page !== normalized.page ||
    result.pagination.page_size !== normalized.page_size
  ) {
    invalidResponse()
  }
  return result
}

export function modelPriceCollectionQueryOptions(
  client: ApiClient,
  filters: MaybeRefOrGetter<ModelPriceFilters>,
) {
  return queryOptions({
    queryKey: computed(() =>
      controlQueryKeys.modelPriceCollection(normalizeModelPriceFilters(toValue(filters))),
    ),
    queryFn: ({ queryKey, signal }) => listModelPrices(client, queryKey[3], signal),
    placeholderData: keepPreviousData,
  })
}

export async function updateModelPrice(
  client: ApiClient,
  id: number,
  request: ModelPriceUpdateRequest,
  signal?: AbortSignal,
): Promise<ModelPriceDto> {
  return projectModelPrice(
    await client.request(`/api/model-prices/${id}`, {
      method: 'PUT',
      json: Object.fromEntries(updateFields.map((field) => [field, request[field]])),
      signal,
    }),
  )
}

export async function resetModelPrice(
  client: ApiClient,
  id: number,
  signal?: AbortSignal,
): Promise<ModelPriceDto> {
  return projectModelPrice(
    await client.request(`/api/model-prices/${id}/reset`, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
}

export function deleteModelPrice(
  client: ApiClient,
  id: number,
  signal?: AbortSignal,
): Promise<void> {
  return client.request<void>(`/api/model-prices/${id}`, { method: 'DELETE', signal })
}

function projectIssueID(data: Record<string, unknown>): number {
  return projectSafeInteger(data.id, { minimum: 1 })
}

export function projectModelPriceMutationIssue(
  error: unknown,
): ModelPriceMutationIssue | undefined {
  if (!(error instanceof ApiError)) return undefined
  if (
    error.code !== 'MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED' &&
    error.code !== 'MODEL_PRICE_REFERENCED' &&
    error.code !== 'MODEL_PRICE_AUTOMATIC_DELETE_FORBIDDEN'
  ) {
    return undefined
  }
  const data = projectRecord(error.data)
  if (error.code === 'MODEL_PRICE_REFERENCED') {
    assertNoSecretLikeFields(data, ['id', 'reference_count', 'reference_group_count'])
    const referenceCount = projectSafeInteger(data.reference_count, { minimum: 1 })
    const referenceGroupCount = projectSafeInteger(data.reference_group_count, {
      minimum: 1,
      maximum: referenceCount,
    })
    return {
      code: error.code,
      id: projectIssueID(data),
      reference_count: referenceCount,
      reference_group_count: referenceGroupCount,
    }
  }
  assertNoSecretLikeFields(data, ['id'])
  return { code: error.code, id: projectIssueID(data) }
}
