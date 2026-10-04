import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'

function record(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value))
    throw new InvalidResponseError()
  return value as Record<string, unknown>
}
function text(value: unknown): string {
  if (typeof value !== 'string') throw new InvalidResponseError()
  return value
}
function integer(value: unknown, min = 0): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min)
    throw new InvalidResponseError()
  return value
}
function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new InvalidResponseError()
  return value
}
function oneOf<T extends string>(value: unknown, values: readonly T[]): T {
  if (!values.includes(value as T)) throw new InvalidResponseError()
  return value as T
}
function list(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new InvalidResponseError()
  return value
}

export const modelsKey = ['control', 'models'] as const
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
export type ModelProfileField = (typeof modelProfileFields)[number]
export type ModelReasoningLevel = (typeof modelReasoningLevels)[number]
export type ModelInputModality = (typeof modelInputModalities)[number]
export type ModelServiceTier = (typeof modelServiceTiers)[number]
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
