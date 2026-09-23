import {
  projectRecord,
  projectString,
  projectBoolean,
  projectSafeInteger,
  projectArray,
} from './projector'

export interface AutoPresetDto {
  id: string
  name: string
  description: string
  model: string
  parameter_overrides: unknown[]
}
export interface AutoEntryDto {
  id: string
  name: string
  enabled: boolean
  fallback: string
  presets: AutoPresetDto[]
}
export interface AutoModelConfigDto {
  enabled: boolean
  model: string
  timeout_seconds: number
  models: AutoEntryDto[]
}
export const defaultAutoModel = (): AutoModelConfigDto => ({
  enabled: false,
  model: '',
  timeout_seconds: 2,
  models: [],
})
export function projectAutoEntry(value: unknown): AutoEntryDto {
  const row = projectRecord(value)
  return {
    id: projectString(row.id),
    name: projectString(row.name),
    enabled: projectBoolean(row.enabled),
    fallback: projectString(row.fallback),
    presets: projectArray(row.presets, (value) => {
      const preset = projectRecord(value)
      return {
        id: projectString(preset.id),
        name: projectString(preset.name),
        description: projectString(preset.description),
        model: projectString(preset.model),
        parameter_overrides: projectArray(preset.parameter_overrides, (rule) =>
          projectRecord(rule),
        ),
      }
    }),
  }
}
export function projectAutoModel(value: unknown): AutoModelConfigDto {
  if (value === undefined) return defaultAutoModel()
  const row = projectRecord(value)
  return {
    enabled: projectBoolean(row.enabled),
    model: projectString(row.model, { allowEmpty: true }),
    timeout_seconds: projectSafeInteger(row.timeout_seconds, { minimum: 1, maximum: 60 }),
    models: projectArray(row.models, projectAutoEntry),
  }
}

export function validAutoModel(value: AutoModelConfigDto): boolean {
  try {
    return (
      Number.isInteger(value.timeout_seconds) &&
      value.timeout_seconds >= 1 &&
      value.timeout_seconds <= 60 &&
      value.models.every(
        (entry) =>
          entry.name.trim() &&
          entry.presets.length > 0 &&
          entry.presets.some((preset) => preset.id === entry.fallback) &&
          entry.presets.every(
            (preset) =>
              preset.name.trim() &&
              preset.description.trim() &&
              preset.model.trim() &&
              Array.isArray(preset.parameter_overrides),
          ),
      )
    )
  } catch {
    return false
  }
}
