import {
  modelInputModalities,
  modelProfileFields,
  modelServiceTiers,
  resolveDefaultReasoningLevel,
  type ModelInputModality,
  type ModelProfile,
  type ModelProfileField,
  type ModelProfileOverrides,
  type ModelProfileValues,
  type ModelReasoningLevel,
} from '@modern/api/models'

export type ModelProfileDraftValues = Omit<
  ModelProfileValues,
  'context_window' | 'auto_compact_token_limit' | 'default_reasoning_level'
> & {
  context_window: string
  auto_compact_token_limit: string
  default_reasoning_level: ModelReasoningLevel | ''
}

export interface ModelProfileDraft {
  custom: Record<ModelProfileField, boolean>
  values: ModelProfileDraftValues
}

export type ModelProfileDraftError =
  | 'invalidDisplayName'
  | 'invalidDescription'
  | 'invalidContextWindow'
  | 'invalidAutoCompactTokenLimit'
  | 'invalidReasoningLevels'
  | 'invalidModalities'
  | 'invalidServiceTiers'
  | 'invalidDefaultReasoningLevel'

const owns = (value: object, key: PropertyKey) => Object.prototype.hasOwnProperty.call(value, key)

export function createModelProfileDraft(profile: ModelProfile): ModelProfileDraft {
  return {
    custom: Object.fromEntries(
      modelProfileFields.map((field) => [field, owns(profile.overrides, field)]),
    ) as Record<ModelProfileField, boolean>,
    values: {
      display_name: profile.effective.display_name,
      description: profile.effective.description,
      context_window:
        profile.effective.context_window === null ? '' : String(profile.effective.context_window),
      auto_compact_token_limit:
        profile.effective.auto_compact_token_limit === null
          ? ''
          : String(profile.effective.auto_compact_token_limit),
      supported_reasoning_levels: [...profile.effective.supported_reasoning_levels],
      default_reasoning_level: profile.effective.default_reasoning_level,
      input_modalities: [...profile.effective.input_modalities],
      service_tiers: [...profile.effective.service_tiers],
    },
  }
}

export function modelProfileDraftOverrides(draft: ModelProfileDraft): ModelProfileOverrides {
  const overrides: ModelProfileOverrides = {}
  if (draft.custom.display_name) overrides.display_name = draft.values.display_name
  if (draft.custom.description) overrides.description = draft.values.description
  if (draft.custom.context_window)
    overrides.context_window = Number(String(draft.values.context_window).trim())
  if (draft.custom.auto_compact_token_limit)
    overrides.auto_compact_token_limit = Number(draft.values.auto_compact_token_limit.trim())
  if (draft.custom.supported_reasoning_levels)
    overrides.supported_reasoning_levels = [...draft.values.supported_reasoning_levels]
  if (draft.custom.default_reasoning_level && draft.values.default_reasoning_level !== '')
    overrides.default_reasoning_level = draft.values.default_reasoning_level
  if (draft.custom.input_modalities) overrides.input_modalities = [...draft.values.input_modalities]
  if (draft.custom.service_tiers) overrides.service_tiers = [...draft.values.service_tiers]
  return overrides
}

export function modelProfileDraftErrors(
  draft: ModelProfileDraft,
): Partial<Record<ModelProfileField, ModelProfileDraftError>> {
  const errors: Partial<Record<ModelProfileField, ModelProfileDraftError>> = {}
  if (
    draft.custom.display_name &&
    (!draft.values.display_name.trim() ||
      new TextEncoder().encode(draft.values.display_name).length > 512)
  )
    errors.display_name = 'invalidDisplayName'
  if (draft.custom.description && new TextEncoder().encode(draft.values.description).length > 4096)
    errors.description = 'invalidDescription'
  if (draft.custom.context_window) {
    const value = String(draft.values.context_window).trim()
    const contextWindow = Number(value)
    if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(contextWindow))
      errors.context_window = 'invalidContextWindow'
  }
  if (draft.custom.auto_compact_token_limit) {
    const value = draft.values.auto_compact_token_limit.trim()
    if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value)))
      errors.auto_compact_token_limit = 'invalidAutoCompactTokenLimit'
  }
  if (draft.custom.supported_reasoning_levels && !draft.values.supported_reasoning_levels.length)
    errors.supported_reasoning_levels = 'invalidReasoningLevels'
  if (
    draft.custom.input_modalities &&
    (!draft.values.input_modalities.length ||
      !draft.values.input_modalities.includes('text') ||
      draft.values.input_modalities.some(
        (value) => !modelInputModalities.includes(value as ModelInputModality),
      ))
  )
    errors.input_modalities = 'invalidModalities'
  if (
    draft.custom.service_tiers &&
    (new Set(draft.values.service_tiers).size !== draft.values.service_tiers.length ||
      draft.values.service_tiers.some((tier) => !modelServiceTiers.includes(tier)))
  )
    errors.service_tiers = 'invalidServiceTiers'
  if (
    draft.values.supported_reasoning_levels.length &&
    (draft.values.default_reasoning_level === '' ||
      !draft.values.supported_reasoning_levels.includes(draft.values.default_reasoning_level))
  )
    errors.default_reasoning_level = 'invalidDefaultReasoningLevel'
  return errors
}

function restoreAutomaticValue(
  draft: ModelProfileDraft,
  automatic: ModelProfileValues,
  field: ModelProfileField,
): void {
  switch (field) {
    case 'display_name':
      draft.values.display_name = automatic.display_name
      break
    case 'description':
      draft.values.description = automatic.description
      break
    case 'context_window':
      draft.values.context_window =
        automatic.context_window === null ? '' : String(automatic.context_window)
      break
    case 'auto_compact_token_limit':
      draft.values.auto_compact_token_limit =
        automatic.auto_compact_token_limit === null
          ? ''
          : String(automatic.auto_compact_token_limit)
      break
    case 'supported_reasoning_levels':
      draft.values.supported_reasoning_levels = [...automatic.supported_reasoning_levels]
      break
    case 'default_reasoning_level':
      draft.values.default_reasoning_level = automatic.default_reasoning_level
      break
    case 'input_modalities':
      draft.values.input_modalities = [...automatic.input_modalities]
      break
    case 'service_tiers':
      draft.values.service_tiers = [...automatic.service_tiers]
      break
  }
}

export function resetModelProfileField(
  profile: ModelProfile,
  draft: ModelProfileDraft,
  field: ModelProfileField,
): void {
  draft.custom[field] = false
  restoreAutomaticValue(draft, profile.automatic, field)
  if (field === 'supported_reasoning_levels' || field === 'default_reasoning_level')
    syncModelProfileReasoningDefault(profile, draft)
}

export function syncModelProfileReasoningDefault(
  profile: ModelProfile,
  draft: ModelProfileDraft,
): void {
  const preferred = draft.custom.default_reasoning_level
    ? draft.values.default_reasoning_level
    : profile.automatic.default_reasoning_level
  draft.values.default_reasoning_level = resolveDefaultReasoningLevel(
    draft.values.supported_reasoning_levels,
    preferred,
  )
}

export function resetModelProfileDraft(profile: ModelProfile, draft: ModelProfileDraft): void {
  for (const field of modelProfileFields) resetModelProfileField(profile, draft, field)
}
