import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'
import { sortProtocols } from '@modern/i18n/protocols'

export const modelsKey = ['modern', 'models'] as const
export const modelContextKey = (model: string, groups: string) =>
  [...modelsKey, 'context', model, groups] as const
export const modelSourceKey = (id: number) => [...modelsKey, 'source', id] as const
export const priceFields = ['input', 'output', 'cache_read', 'cache_write'] as const
export const modelProfileFields = [
  'display_name',
  'description',
  'context_window',
  'auto_compact_token_limit',
  'supported_reasoning_levels',
  'default_reasoning_level',
  'input_modalities',
  'service_tiers',
] as const
export const modelReasoningLevels = [
  'none',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
  'ultra',
  'persistent',
] as const
export const modelInputModalities = ['text', 'image', 'audio'] as const
export const modelServiceTiers = ['priority', 'ultrafast'] as const
export type PriceField = (typeof priceFields)[number]
export type ModelProfileField = (typeof modelProfileFields)[number]
export type ModelReasoningLevel = (typeof modelReasoningLevels)[number]
export type ModelInputModality = (typeof modelInputModalities)[number]
export type ModelServiceTier = (typeof modelServiceTiers)[number]
export type PriceSlots = Record<PriceField, string | null>
export interface PriceTier {
  threshold_tokens: number
  prices: PriceSlots
}
export interface PriceSchedule {
  prices: PriceSlots
  context_tiers: PriceTier[]
}
export interface ModelPrice extends PriceSchedule {
  id: number
  model: string
  channel: { id: string; name: string; icon: string; mark: string }
  mode_schedules: Record<string, PriceSchedule>
  status: 'pending' | 'configured'
  method: 'auto_sync' | 'user_set' | 'user_marked_unpriced' | null
  provider: string | null
  canReset: boolean
  updatedAt: number
}
export interface ModelGroup {
  id: number
  name: string
  enabled: boolean
  protocols: string[]
}
export interface ModelCatalog {
  source: 'actual_provider' | 'reference_provider'
  provider: string
  name: string
  description: string
  family: string
  capabilities: Record<string, boolean | null>
  modalities: { input: string[]; output: string[] }
  limits: { context: number | null; input: number | null; output: number | null }
  releaseDate: string
  knowledge: string
  status: string
}
export interface ModelSource {
  model: string
  price: ModelPrice
  groups: ModelGroup[]
  affectedGroups: ModelGroup[]
  catalog: ModelCatalog | null
}
export interface RequestModel {
  name: string
  protocols: string[]
  sources: ModelSource[]
}
export interface ModelProfileValues {
  display_name: string
  description: string
  context_window: number | null
  auto_compact_token_limit: number | null
  supported_reasoning_levels: ModelReasoningLevel[]
  default_reasoning_level: ModelReasoningLevel
  input_modalities: ModelInputModality[]
  service_tiers: ModelServiceTier[]
}
export type ModelProfileOverrides = Partial<{
  [Field in ModelProfileField]: ModelProfileValues[Field]
}>
export interface ModelProfile {
  clientModel: string
  automatic: ModelProfileValues
  overrides: ModelProfileOverrides
  effective: ModelProfileValues
  hasOverrides: boolean
}
export interface ModelSourceDetail {
  model: string
  price: ModelPrice
  catalog: ModelCatalog | null
  associations: { model: string; group: ModelGroup }[]
  groupCount: number
  modelCount: number
}
export interface ModelFilters {
  q: string
  groups: 'enabled' | 'all'
  pricing: 'all' | 'pending' | 'configured'
  page: number
  pageSize: number
}
function decimal(value: unknown): string | null {
  if (value === null) return null
  const result = text(value)
  if (!/^\d+(\.\d{1,9})?$/.test(result)) throw new InvalidResponseError()
  return result
}
function slots(value: unknown): PriceSlots {
  const row = record(value)
  return {
    input: decimal(row.input),
    output: decimal(row.output),
    cache_read: decimal(row.cache_read),
    cache_write: decimal(row.cache_write),
  }
}
function schedule(value: unknown): PriceSchedule {
  const row = record(value)
  return {
    prices: slots(row.prices),
    context_tiers: list(row.context_tiers).map((value) => {
      const tier = record(value)
      return { threshold_tokens: integer(tier.threshold_tokens), prices: slots(tier.prices) }
    }),
  }
}
export function readModelPrice(value: unknown): ModelPrice {
  const row = record(value)
  return {
    ...schedule(row),
    id: integer(row.id, 1),
    model: text(row.model_id),
    channel: {
      id: text(row.channel_id),
      name: text(row.channel_name),
      icon: text(row.channel_icon),
      mark: text(row.channel_mark),
    },
    mode_schedules: Object.fromEntries(
      Object.entries(record(row.mode_schedules)).map(([mode, value]) => [mode, schedule(value)]),
    ),
    status: oneOf(row.pricing_status, ['pending', 'configured']),
    method:
      row.method === null
        ? null
        : (oneOf(row.method, [
            'auto_sync',
            'user_set',
            'user_marked_unpriced',
          ]) as ModelPrice['method']),
    provider: row.matched_provider_id === null ? null : text(row.matched_provider_id),
    canReset: boolean(row.can_reset),
    updatedAt: integer(row.updated_at_ms),
  }
}
function group(value: unknown): ModelGroup {
  const row = record(value)
  return {
    id: integer(row.id, 1),
    name: text(row.name),
    enabled: boolean(row.enabled),
    protocols: sortProtocols(list(row.client_protocols).map(text)),
  }
}
function catalog(value: unknown): ModelCatalog | null {
  if (value === null) return null
  const ref = record(value)
  const model = record(ref.model)
  const capabilities = record(model.capabilities)
  const limits = record(model.limits)
  const modalities = record(model.modalities)
  return {
    source: oneOf(ref.source, ['actual_provider', 'reference_provider']),
    provider: text(ref.provider_name),
    name: text(model.name),
    description: text(model.description),
    family: text(model.family),
    capabilities: {
      ...Object.fromEntries(
        ['attachment', 'reasoning', 'tool_call', 'structured_output', 'temperature'].map((key) => [
          key,
          capabilities[key] === null ? null : boolean(capabilities[key]),
        ]),
      ),
      open_weights: model.open_weights === null ? null : boolean(model.open_weights),
    },
    modalities: {
      input: list(modalities.input).map(text),
      output: list(modalities.output).map(text),
    },
    limits: {
      context: limits.context === null ? null : integer(limits.context),
      input: limits.input === null ? null : integer(limits.input),
      output: limits.output === null ? null : integer(limits.output),
    },
    releaseDate: text(model.release_date),
    knowledge: text(model.knowledge),
    status: text(model.status),
  }
}
function uniqueOptions<T extends string>(value: unknown, options: readonly T[]): T[] {
  const values = list(value).map((item) => oneOf(item, options))
  if (new Set(values).size !== values.length) throw new InvalidResponseError()
  return values
}
function inputModalities(value: unknown): ModelInputModality[] {
  const values = uniqueOptions(value, modelInputModalities)
  if (!values.length || !values.includes('text')) throw new InvalidResponseError()
  return values
}
export function resolveDefaultReasoningLevel(
  levels: readonly ModelReasoningLevel[],
  preferred: string | undefined,
): ModelReasoningLevel | '' {
  const selected = levels.find((level) => level === preferred)
  if (selected) return selected
  return levels.includes('medium') ? 'medium' : (levels[0] ?? '')
}
function profileValues(value: unknown): ModelProfileValues {
  const row = record(value)
  const supportedReasoningLevels = uniqueOptions(
    row.supported_reasoning_levels,
    modelReasoningLevels,
  )
  if (!supportedReasoningLevels.length) throw new InvalidResponseError()
  const defaultReasoningLevel =
    row.default_reasoning_level == null
      ? resolveDefaultReasoningLevel(supportedReasoningLevels, undefined)
      : oneOf(row.default_reasoning_level, modelReasoningLevels)
  if (defaultReasoningLevel === '' || !supportedReasoningLevels.includes(defaultReasoningLevel))
    throw new InvalidResponseError()
  return {
    display_name: text(row.display_name),
    description: row.description == null ? '' : text(row.description),
    context_window: row.context_window === null ? null : integer(row.context_window, 1),
    auto_compact_token_limit:
      row.auto_compact_token_limit == null ? null : integer(row.auto_compact_token_limit, 1),
    supported_reasoning_levels: supportedReasoningLevels,
    default_reasoning_level: defaultReasoningLevel,
    input_modalities: inputModalities(row.input_modalities),
    service_tiers:
      row.service_tiers == null ? [] : uniqueOptions(row.service_tiers, modelServiceTiers),
  }
}
function profileOverrides(value: unknown): ModelProfileOverrides {
  const row = record(value)
  const overrides: ModelProfileOverrides = {}
  if (row.display_name !== undefined && row.display_name !== null)
    overrides.display_name = text(row.display_name)
  if (row.description !== undefined && row.description !== null)
    overrides.description = text(row.description)
  if (row.context_window !== undefined && row.context_window !== null)
    overrides.context_window = integer(row.context_window, 1)
  if (row.auto_compact_token_limit !== undefined && row.auto_compact_token_limit !== null)
    overrides.auto_compact_token_limit = integer(row.auto_compact_token_limit, 1)
  if (row.supported_reasoning_levels !== undefined && row.supported_reasoning_levels !== null)
    overrides.supported_reasoning_levels = uniqueOptions(
      row.supported_reasoning_levels,
      modelReasoningLevels,
    )
  if (row.default_reasoning_level !== undefined && row.default_reasoning_level !== null)
    overrides.default_reasoning_level = oneOf(row.default_reasoning_level, modelReasoningLevels)
  if (row.input_modalities !== undefined && row.input_modalities !== null)
    overrides.input_modalities = inputModalities(row.input_modalities)
  if (row.service_tiers !== undefined && row.service_tiers !== null)
    overrides.service_tiers = uniqueOptions(row.service_tiers, modelServiceTiers)
  return overrides
}
export function readModelProfile(value: unknown): ModelProfile {
  const row = record(value)
  return {
    clientModel: text(row.client_model),
    automatic: profileValues(row.automatic),
    overrides: profileOverrides(row.overrides),
    effective: profileValues(row.effective),
    hasOverrides: boolean(row.has_overrides),
  }
}
function source(value: unknown): ModelSource {
  const row = record(value)
  const price = readModelPrice(row.price)
  const model = text(row.model_id)
  if (price.model !== model) throw new InvalidResponseError()
  return {
    model,
    price,
    groups: list(row.route_groups).map(group),
    affectedGroups: list(row.affected_groups).map(group),
    catalog: catalog(row.catalog_reference),
  }
}
export async function getModels(client: ApiClient, filters: ModelFilters, signal: AbortSignal) {
  const params = new URLSearchParams({
    group_status: filters.groups,
    pricing_status: filters.pricing,
    page: String(filters.page),
    page_size: String(filters.pageSize),
  })
  if (filters.q) params.set('q', filters.q)
  const path = `/api/models?${params}` as const
  const row = record(await client.request(path, { signal }))
  const pagination = record(row.pagination)
  if (pagination.page !== filters.page || pagination.page_size !== filters.pageSize)
    throw new InvalidResponseError()
  return {
    items: list(row.items).map((value): RequestModel => {
      const model = record(value)
      return {
        name: text(model.client_model),
        protocols: sortProtocols(list(model.protocols).map(text)),
        sources: list(model.upstream_models).map(source),
      }
    }),
    total: integer(pagination.total_items),
    pages: integer(pagination.total_pages),
  }
}
// 详情不依赖列表当前页或计价筛选，硬刷新与跨页进入同样能取到完整来源。
export async function getModelContext(
  client: ApiClient,
  name: string,
  groups: ModelFilters['groups'],
  signal: AbortSignal,
): Promise<RequestModel | null> {
  for (let page = 1; ; page++) {
    const result = await getModels(
      client,
      { q: Array.from(name).slice(0, 200).join(''), groups, pricing: 'all', page, pageSize: 100 },
      signal,
    )
    const model = result.items.find((item) => item.name === name)
    if (model) return model
    if (page >= result.pages) return null
  }
}
export async function getModelSource(
  client: ApiClient,
  id: number,
  signal: AbortSignal,
): Promise<ModelSourceDetail> {
  const path = `/api/model-prices/${id}` as const
  const row = record(await client.request(path, { signal }))
  const price = readModelPrice(row.price)
  if (price.id !== id || price.model !== row.model_id) throw new InvalidResponseError()
  return {
    model: text(row.model_id),
    price,
    catalog: catalog(row.catalog_reference),
    associations: list(row.associations).map((value) => {
      const item = record(value)
      return { model: text(item.client_model), group: group(item.group) }
    }),
    groupCount: integer(row.group_count),
    modelCount: integer(row.client_model_count),
  }
}
export interface ModelPriceUpdate extends PriceSlots {
  context_tiers: (PriceSlots & { threshold_tokens: number })[]
  mode_schedules: Record<
    string,
    { prices: PriceSlots; context_tiers: (PriceSlots & { threshold_tokens: number })[] }
  >
  confirm_unpriced: boolean
}
export async function saveModelPrice(
  client: ApiClient,
  id: number,
  patch: ModelPriceUpdate,
  signal: AbortSignal,
): Promise<ModelPrice> {
  const path = `/api/model-prices/${id}` as const
  return readModelPrice(await client.request(path, { method: 'PUT', json: patch, signal }))
}
export async function resetModelPrice(client: ApiClient, id: number, signal: AbortSignal) {
  const path = `/api/model-prices/${id}/reset` as const
  return readModelPrice(
    await client.request(path, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
}
export async function syncModelPrices(client: ApiClient, signal: AbortSignal): Promise<void> {
  const result = record(await client.request('/api/model-prices/sync', { method: 'POST', signal }))
  if (result.error_code) throw new InvalidResponseError()
}

export const clientCatalogKey = [...modelsKey, 'client-catalog'] as const
export interface ClientCatalogBudget {
  limitBytes: number
  responseBytes: number
  selectedBytes: number
  includedCount: number
  entries: { model: string; bytes: number; included: boolean }[]
}
export interface ClientCatalog {
  models: ModelProfile[]
  selected: string[]
  defaults: string[]
  budget: ClientCatalogBudget
  clientVersion: string
}
export interface ClientCatalogDraft {
  known_models: string[]
  models?: string[]
  reset_directory?: boolean
  profiles: { client_model: string; overrides: ModelProfileOverrides }[]
}
function readClientCatalog(value: unknown): ClientCatalog {
  const row = record(value)
  const budget = record(row.budget)
  const models = list(row.models).map(readModelProfile)
  const selected = list(row.selected).map(text)
  const defaults = list(row.defaults).map(text)
  const names = new Set(models.map((model) => model.clientModel))
  if (
    names.size !== models.length ||
    new Set(selected).size !== selected.length ||
    selected.some((model) => !names.has(model)) ||
    defaults.some((model) => !names.has(model))
  )
    throw new InvalidResponseError()
  const entries = list(budget.entries).map((value) => {
    const entry = record(value)
    return {
      model: text(entry.client_model),
      bytes: integer(entry.bytes),
      included: boolean(entry.included),
    }
  })
  const includedCount = integer(budget.included_count)
  if (
    entries.length !== selected.length ||
    entries.some(
      (entry, index) => entry.model !== selected[index] || entry.included !== index < includedCount,
    )
  )
    throw new InvalidResponseError()
  return {
    models,
    selected,
    defaults,
    clientVersion: text(row.client_version),
    budget: {
      limitBytes: integer(budget.limit_bytes, 1),
      responseBytes: integer(budget.response_bytes),
      selectedBytes: integer(budget.selected_bytes),
      includedCount,
      entries,
    },
  }
}
export async function getClientCatalog(
  client: ApiClient,
  signal: AbortSignal,
): Promise<ClientCatalog> {
  return readClientCatalog(await client.request('/api/models/client-catalog', { signal }))
}
export async function previewClientCatalog(
  client: ApiClient,
  draft: ClientCatalogDraft,
  signal: AbortSignal,
): Promise<ClientCatalog> {
  return readClientCatalog(
    await client.request('/api/models/client-catalog/preview', {
      method: 'POST',
      json: draft,
      signal,
    }),
  )
}
export async function saveClientCatalog(
  client: ApiClient,
  draft: ClientCatalogDraft,
  signal: AbortSignal,
): Promise<ClientCatalog> {
  return readClientCatalog(
    await client.request('/api/models/client-catalog', { method: 'PUT', json: draft, signal }),
  )
}
