import {
  modelInputModalities,
  modelProfileFields,
  type ModelInputModality,
  type ModelProfile,
  type ModelProfileField,
  type ModelProfileOverrides,
  type ModelProfileValues,
} from '@modern/api/models'

export type ModelProfileDraftValues = Omit<ModelProfileValues, 'context_window'> & {
  context_window: string
}

export interface ModelProfileDraft {
  custom: Record<ModelProfileField, boolean>
  values: ModelProfileDraftValues
}

export type ModelProfileDraftError =
  'invalidContextWindow' | 'invalidReasoningLevels' | 'invalidModalities'

const owns = (value: object, key: PropertyKey) => Object.prototype.hasOwnProperty.call(value, key)

export function createModelProfileDraft(profile: ModelProfile): ModelProfileDraft {
  return {
    custom: Object.fromEntries(
      modelProfileFields.map((field) => [field, owns(profile.overrides, field)]),
    ) as Record<ModelProfileField, boolean>,
    values: {
      display_name: profile.effective.display_name,
      context_window:
        profile.effective.context_window === null ? '' : String(profile.effective.context_window),
      supported_reasoning_levels: [...profile.effective.supported_reasoning_levels],
      input_modalities: [...profile.effective.input_modalities],
    },
  }
}

export function modelProfileDraftOverrides(draft: ModelProfileDraft): ModelProfileOverrides {
  const overrides: ModelProfileOverrides = {}
  if (draft.custom.display_name) overrides.display_name = draft.values.display_name
  if (draft.custom.context_window)
    overrides.context_window = Number(String(draft.values.context_window).trim())
  if (draft.custom.supported_reasoning_levels)
    overrides.supported_reasoning_levels = [...draft.values.supported_reasoning_levels]
  if (draft.custom.input_modalities) overrides.input_modalities = [...draft.values.input_modalities]
  return overrides
}

export function modelProfileDraftErrors(
  draft: ModelProfileDraft,
): Partial<Record<ModelProfileField, ModelProfileDraftError>> {
  const errors: Partial<Record<ModelProfileField, ModelProfileDraftError>> = {}
  if (draft.custom.context_window) {
    const value = String(draft.values.context_window).trim()
    const contextWindow = Number(value)
    if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(contextWindow))
      errors.context_window = 'invalidContextWindow'
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
    case 'context_window':
      draft.values.context_window =
        automatic.context_window === null ? '' : String(automatic.context_window)
      break
    case 'supported_reasoning_levels':
      draft.values.supported_reasoning_levels = [...automatic.supported_reasoning_levels]
      break
    case 'input_modalities':
      draft.values.input_modalities = [...automatic.input_modalities]
      break
  }
}

export function setModelProfileFieldMode(
  profile: ModelProfile,
  draft: ModelProfileDraft,
  field: ModelProfileField,
  custom: boolean,
): void {
  draft.custom[field] = custom
  if (!custom) restoreAutomaticValue(draft, profile.automatic, field)
}

export function resetModelProfileDraft(profile: ModelProfile, draft: ModelProfileDraft): void {
  for (const field of modelProfileFields) setModelProfileFieldMode(profile, draft, field, false)
}
