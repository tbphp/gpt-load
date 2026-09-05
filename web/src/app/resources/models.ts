import { keepPreviousData, queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { ChannelParamsDto, GroupProtocol } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { projectModelPrice, type ModelPriceDto } from '@/app/resources/model-prices'
import { projectChannelID } from '@/app/resources/channels'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type ModelCollectionGroupStatus = 'enabled' | 'all'
export type ModelCollectionPricingStatus = 'pending' | 'configured' | 'all'
export type ModelCollectionPageSize = 10

export interface ModelCollectionFilters {
  group_status: ModelCollectionGroupStatus
  pricing_status: ModelCollectionPricingStatus
  q?: string
  page: number
  page_size: ModelCollectionPageSize
}

export interface ModelCatalogCapabilitiesDto {
  attachment: boolean | null
  reasoning: boolean | null
  tool_call: boolean | null
  structured_output: boolean | null
  temperature: boolean | null
}

export interface ModelCatalogModalitiesDto {
  input: string[]
  output: string[]
}

export interface ModelCatalogLimitsDto {
  context: number | null
  input: number | null
  output: number | null
}

export interface ModelCatalogMetadataDto {
  id: string
  name: string
  description: string
  family: string
  modalities: ModelCatalogModalitiesDto
  limits: ModelCatalogLimitsDto
  capabilities: ModelCatalogCapabilitiesDto
  release_date: string
  last_updated: string
  knowledge: string
  open_weights: boolean | null
  status: string
}

export interface ModelRouteGroupDto {
  id: number
  name: string
  channel_id: string
  params: ChannelParamsDto
  enabled: boolean
  client_protocols: GroupProtocol[]
}

export type ModelCatalogReferenceSource = 'actual_provider' | 'reference_provider'

export interface ModelCatalogReferenceDto {
  source: ModelCatalogReferenceSource
  provider_id: string
  provider_name: string
  model: ModelCatalogMetadataDto
}

export interface ModelUpstreamDto {
  model_id: string
  alias_applied: boolean
  price: ModelPriceDto
  route_groups: ModelRouteGroupDto[]
  affected_groups: ModelRouteGroupDto[]
  catalog_reference: ModelCatalogReferenceDto | null
}

export interface UpstreamModelAssociationDto {
  client_model: string
  alias_applied: boolean
  group: ModelRouteGroupDto
}

export interface UpstreamModelDetailDto {
  model_id: string
  price: ModelPriceDto
  catalog_reference: ModelCatalogReferenceDto | null
  associations: UpstreamModelAssociationDto[]
  client_model_count: number
  group_count: number
}

export interface ClientModelDto {
  client_model: string
  protocols: GroupProtocol[]
  upstream_models: ModelUpstreamDto[]
}

export interface ModelCollectionPaginationDto {
  page: number
  page_size: number
  total_items: number
  total_pages: number
}

export interface ModelCollectionSummaryDto {
  client_model_count: number
  upstream_model_count: number
  price_count: number
  pending_price_count: number
  unreferenced_price_count: number
}

export interface ModelCollectionCatalogDto {
  available: boolean
  checked_at_ms: number
  successful_fetch_at_ms: number
  error_code: string
}

export interface ModelCollectionDto {
  summary: ModelCollectionSummaryDto
  catalog: ModelCollectionCatalogDto
  items: ClientModelDto[]
  pagination: ModelCollectionPaginationDto
}

const collectionFields = ['summary', 'catalog', 'items', 'pagination'] as const
const clientModelFields = ['client_model', 'protocols', 'upstream_models'] as const
const upstreamModelFields = [
  'model_id',
  'alias_applied',
  'price',
  'route_groups',
  'affected_groups',
  'catalog_reference',
] as const
const upstreamDetailFields = [
  'model_id',
  'price',
  'catalog_reference',
  'associations',
  'client_model_count',
  'group_count',
] as const
const associationFields = ['client_model', 'alias_applied', 'group'] as const
const catalogReferenceFields = ['source', 'provider_id', 'provider_name', 'model'] as const
const groupFields = ['id', 'name', 'channel_id', 'params', 'enabled', 'client_protocols'] as const
const catalogModelFields = [
  'id',
  'name',
  'description',
  'family',
  'modalities',
  'limits',
  'capabilities',
  'release_date',
  'last_updated',
  'knowledge',
  'open_weights',
  'status',
] as const
const modalitiesFields = ['input', 'output'] as const
const limitsFields = ['context', 'input', 'output'] as const
const capabilitiesFields = [
  'attachment',
  'reasoning',
  'tool_call',
  'structured_output',
  'temperature',
] as const
const paginationFields = ['page', 'page_size', 'total_items', 'total_pages'] as const
const summaryFields = [
  'client_model_count',
  'upstream_model_count',
  'price_count',
  'pending_price_count',
  'unreferenced_price_count',
] as const
const catalogFields = [
  'available',
  'checked_at_ms',
  'successful_fetch_at_ms',
  'error_code',
] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectIdentityString(value: unknown, allowEmpty = false): string {
  const result = projectString(value, { allowEmpty })
  if (result !== result.trim() || /\p{Cc}/u.test(result) || (!allowEmpty && result.length === 0)) {
    invalidResponse()
  }
  return result
}

function projectDisplayString(value: unknown, allowEmpty = false): string {
  const result = projectString(value, { allowEmpty })
  if (result !== result.trim() || (!allowEmpty && result.length === 0)) invalidResponse()
  return result
}

function projectProtocols(value: unknown): GroupProtocol[] {
  const protocols = projectArray(value, (protocol) => projectEnum(protocol, enabledDataProtocols))
  if (new Set(protocols).size !== protocols.length) invalidResponse()
  return protocols
}

function projectChannelParams(value: unknown): ChannelParamsDto {
  const record = projectRecord(value)
  const result: ChannelParamsDto = {}
  for (const [key, param] of Object.entries(record)) {
    if (!/^[a-z][a-z0-9_]*$/u.test(key)) invalidResponse()
    result[key] = projectString(param, { allowEmpty: true })
  }
  return result
}

function projectNullableBoolean(value: unknown): boolean | null {
  return value === null ? null : projectBoolean(value)
}

function projectNullableLimit(value: unknown): number | null {
  return value === null ? null : projectSafeInteger(value, { minimum: 0 })
}

function projectStringList(value: unknown): string[] {
  const result = projectArray(value, (item) => projectIdentityString(item))
  if (new Set(result).size !== result.length) invalidResponse()
  return result
}

function projectCatalogMetadata(value: unknown, upstreamModelID: string): ModelCatalogMetadataDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, catalogModelFields)

  const id = projectIdentityString(record.id)
  if (id !== upstreamModelID) invalidResponse()

  const modalitiesRecord = projectRecord(record.modalities)
  assertNoSecretLikeFields(modalitiesRecord, modalitiesFields)
  const limitsRecord = projectRecord(record.limits)
  assertNoSecretLikeFields(limitsRecord, limitsFields)
  const capabilitiesRecord = projectRecord(record.capabilities)
  assertNoSecretLikeFields(capabilitiesRecord, capabilitiesFields)

  return {
    id,
    name: projectDisplayString(record.name),
    description: projectDisplayString(record.description, true),
    family: projectIdentityString(record.family, true),
    modalities: {
      input: projectStringList(modalitiesRecord.input),
      output: projectStringList(modalitiesRecord.output),
    },
    limits: {
      context: projectNullableLimit(limitsRecord.context),
      input: projectNullableLimit(limitsRecord.input),
      output: projectNullableLimit(limitsRecord.output),
    },
    capabilities: {
      attachment: projectNullableBoolean(capabilitiesRecord.attachment),
      reasoning: projectNullableBoolean(capabilitiesRecord.reasoning),
      tool_call: projectNullableBoolean(capabilitiesRecord.tool_call),
      structured_output: projectNullableBoolean(capabilitiesRecord.structured_output),
      temperature: projectNullableBoolean(capabilitiesRecord.temperature),
    },
    release_date: projectIdentityString(record.release_date, true),
    last_updated: projectIdentityString(record.last_updated, true),
    knowledge: projectIdentityString(record.knowledge, true),
    open_weights: projectNullableBoolean(record.open_weights),
    status: projectIdentityString(record.status, true),
  }
}

function projectRouteGroup(value: unknown): ModelRouteGroupDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, groupFields)
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectIdentityString(record.name),
    channel_id: projectChannelID(record.channel_id),
    params: projectChannelParams(record.params),
    enabled: projectBoolean(record.enabled),
    client_protocols: projectProtocols(record.client_protocols),
  }
}

function projectCatalogReference(
  value: unknown,
  upstreamModelID: string,
): ModelCatalogReferenceDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, catalogReferenceFields)
  return {
    source: projectEnum(record.source, ['actual_provider', 'reference_provider'] as const),
    provider_id: projectIdentityString(record.provider_id),
    provider_name: projectDisplayString(record.provider_name),
    model: projectCatalogMetadata(record.model, upstreamModelID),
  }
}

function projectUpstreamPrice(
  record: Record<string, unknown>,
  upstreamModelID: string,
  isAccessKey: boolean,
): Pick<ModelUpstreamDto, 'price' | 'route_groups' | 'affected_groups' | 'catalog_reference'> {
  const price = projectModelPrice(record.price)
  const routeGroups = projectArray(record.route_groups, projectRouteGroup)
  const affectedGroups = projectArray(record.affected_groups, projectRouteGroup)
  const affectedGroupIDs = new Set(affectedGroups.map(({ id }) => id))
  if (
    price.model_id !== upstreamModelID ||
    (isAccessKey
      ? routeGroups.length !== 0 || affectedGroups.length !== 0
      : routeGroups.length === 0 ||
        affectedGroups.length === 0 ||
        price.reference_group_count !== affectedGroups.length) ||
    routeGroups.some(({ channel_id }) => channel_id !== price.channel_id) ||
    affectedGroups.some(({ channel_id }) => channel_id !== price.channel_id) ||
    new Set(routeGroups.map(({ id }) => id)).size !== routeGroups.length ||
    affectedGroupIDs.size !== affectedGroups.length ||
    routeGroups.some(({ id }) => !affectedGroupIDs.has(id))
  ) {
    invalidResponse()
  }
  return {
    price,
    route_groups: routeGroups,
    affected_groups: affectedGroups,
    catalog_reference:
      record.catalog_reference === null
        ? null
        : projectCatalogReference(record.catalog_reference, upstreamModelID),
  }
}

function projectUpstreamModel(
  value: unknown,
  clientModel: string,
  isAccessKey: boolean,
): ModelUpstreamDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, upstreamModelFields)
  const modelID = projectIdentityString(record.model_id)
  const aliasApplied = projectBoolean(record.alias_applied)
  if (!aliasApplied && modelID !== clientModel) invalidResponse()
  return {
    model_id: modelID,
    alias_applied: aliasApplied,
    ...projectUpstreamPrice(record, modelID, isAccessKey),
  }
}

function projectAssociation(value: unknown): UpstreamModelAssociationDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, associationFields)
  return {
    client_model: projectIdentityString(record.client_model),
    alias_applied: projectBoolean(record.alias_applied),
    group: projectRouteGroup(record.group),
  }
}

export function projectUpstreamModelDetail(value: unknown): UpstreamModelDetailDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, upstreamDetailFields)
  const modelID = projectIdentityString(record.model_id)
  const price = projectModelPrice(record.price)
  const associations = projectArray(record.associations, projectAssociation)
  const clientModelCount = projectSafeInteger(record.client_model_count, { minimum: 0 })
  const groupCount = projectSafeInteger(record.group_count, { minimum: 0 })
  if (
    price.model_id !== modelID ||
    associations.some(({ group }) => group.channel_id !== price.channel_id) ||
    clientModelCount !== new Set(associations.map(({ client_model }) => client_model)).size ||
    groupCount !== new Set(associations.map(({ group }) => group.id)).size ||
    price.reference_count !== associations.length ||
    price.reference_group_count !== groupCount
  )
    invalidResponse()
  return {
    model_id: modelID,
    price,
    catalog_reference:
      record.catalog_reference === null
        ? null
        : projectCatalogReference(record.catalog_reference, modelID),
    associations,
    client_model_count: clientModelCount,
    group_count: groupCount,
  }
}

function projectClientModel(value: unknown, isAccessKey: boolean): ClientModelDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, clientModelFields)
  const clientModel = projectIdentityString(record.client_model)
  const upstreamModels = projectArray(record.upstream_models, (upstream) =>
    projectUpstreamModel(upstream, clientModel, isAccessKey),
  )
  if (
    upstreamModels.length === 0 ||
    new Set(upstreamModels.map(({ price }) => price.id)).size !== upstreamModels.length
  ) {
    invalidResponse()
  }
  return {
    client_model: clientModel,
    protocols: projectProtocols(record.protocols),
    upstream_models: upstreamModels,
  }
}

function projectPagination(value: unknown): ModelCollectionPaginationDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, paginationFields)
  const pageSize = projectSafeInteger(record.page_size, { minimum: 1, maximum: 100 })
  return {
    page: projectSafeInteger(record.page, { minimum: 1 }),
    page_size: pageSize,
    total_items: projectSafeInteger(record.total_items, { minimum: 0 }),
    total_pages: projectSafeInteger(record.total_pages, { minimum: 0 }),
  }
}

function projectSummary(value: unknown): ModelCollectionSummaryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, summaryFields)
  const summary = {
    client_model_count: projectSafeInteger(record.client_model_count, { minimum: 0 }),
    upstream_model_count: projectSafeInteger(record.upstream_model_count, { minimum: 0 }),
    price_count: projectSafeInteger(record.price_count, { minimum: 0 }),
    pending_price_count: projectSafeInteger(record.pending_price_count, { minimum: 0 }),
    unreferenced_price_count: projectSafeInteger(record.unreferenced_price_count, {
      minimum: 0,
    }),
  }
  if (
    summary.pending_price_count > summary.price_count ||
    summary.upstream_model_count < summary.client_model_count
  ) {
    invalidResponse()
  }
  return summary
}

function projectCatalog(value: unknown): ModelCollectionCatalogDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, catalogFields)
  return {
    available: projectBoolean(record.available),
    checked_at_ms: projectSafeInteger(record.checked_at_ms, { minimum: 0 }),
    successful_fetch_at_ms: projectSafeInteger(record.successful_fetch_at_ms, { minimum: 0 }),
    error_code: projectIdentityString(record.error_code, true),
  }
}

function expectedPageItems(pagination: ModelCollectionPaginationDto): number {
  if (pagination.total_items === 0 || pagination.page > pagination.total_pages) return 0
  if (pagination.page < pagination.total_pages) return pagination.page_size
  const remainder = pagination.total_items % pagination.page_size
  return remainder === 0 ? pagination.page_size : remainder
}

export function projectModelCollection(value: unknown, isAccessKey = false): ModelCollectionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, collectionFields)
  const summary = projectSummary(record.summary)
  const catalog = projectCatalog(record.catalog)
  const items = projectArray(record.items, (item) => projectClientModel(item, isAccessKey))
  const pagination = projectPagination(record.pagination)
  const expectedTotalPages =
    pagination.total_items === 0 ? 0 : Math.ceil(pagination.total_items / pagination.page_size)
  if (
    pagination.total_pages !== expectedTotalPages ||
    items.length !== expectedPageItems(pagination) ||
    new Set(items.map(({ client_model }) => client_model)).size !== items.length ||
    pagination.total_items > summary.client_model_count
  ) {
    invalidResponse()
  }
  return { summary, catalog, items, pagination }
}

export function normalizeModelCollectionFilters(
  filters: ModelCollectionFilters,
): ModelCollectionFilters {
  const normalized: ModelCollectionFilters = {
    group_status: filters.group_status,
    pricing_status: filters.pricing_status,
    page: filters.page,
    page_size: filters.page_size,
  }
  const query = filters.q?.trim()
  if (query) normalized.q = [...query].slice(0, 200).join('')
  return normalized
}

export async function listModels(
  client: ApiClient,
  filters: ModelCollectionFilters,
  signal?: AbortSignal,
  isAccessKey = false,
): Promise<ModelCollectionDto> {
  const normalized = normalizeModelCollectionFilters(filters)
  const params = new URLSearchParams({
    group_status: normalized.group_status,
    pricing_status: normalized.pricing_status,
    page: String(normalized.page),
    page_size: String(normalized.page_size),
  })
  if (normalized.q !== undefined) params.set('q', normalized.q)
  const result = projectModelCollection(
    await client.request(`/api/models?${params.toString()}`, { method: 'GET', signal }),
    isAccessKey,
  )
  if (
    result.pagination.page !== normalized.page ||
    result.pagination.page_size !== normalized.page_size
  ) {
    invalidResponse()
  }
  return result
}

export async function getUpstreamModelDetail(
  client: ApiClient,
  priceID: number,
  signal?: AbortSignal,
): Promise<UpstreamModelDetailDto> {
  return projectUpstreamModelDetail(
    await client.request(`/api/model-prices/${priceID}`, { method: 'GET', signal }),
  )
}

export function modelCollectionQueryOptions(
  client: ApiClient,
  filters: MaybeRefOrGetter<ModelCollectionFilters>,
  isAccessKey: MaybeRefOrGetter<boolean> = false,
) {
  return queryOptions({
    queryKey: computed(() =>
      controlQueryKeys.models.collection(normalizeModelCollectionFilters(toValue(filters))),
    ),
    queryFn: ({ queryKey, signal }) =>
      listModels(client, queryKey[3], signal, toValue(isAccessKey)),
    placeholderData: keepPreviousData,
  })
}
