import { InvalidResponseError } from '@shared/http/errors'
import {
  projectRecord,
  projectString,
  projectBoolean,
  projectSafeInteger,
  projectEnum,
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
  provider: 'typesafe' | 'openrouter'
  model: string
  api_key: string
  api_key_configured?: boolean
  timeout_seconds: number
  input_price: string
  output_price: string
  models: AutoEntryDto[]
}
export const defaultAutoModel = (): AutoModelConfigDto => ({
  enabled: false,
  provider: 'typesafe',
  model: 'jev-latest',
  api_key: '',
  timeout_seconds: 2,
  input_price: '0.042',
  output_price: '0',
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
  if (row.api_key !== '') throw new InvalidResponseError()
  return {
    enabled: projectBoolean(row.enabled),
    provider: projectEnum(row.provider, ['typesafe', 'openrouter']),
    model: projectString(row.model),
    api_key: '',
    api_key_configured: projectBoolean(row.api_key_configured),
    timeout_seconds: projectSafeInteger(row.timeout_seconds, { minimum: 1, maximum: 60 }),
    input_price: projectString(row.input_price),
    output_price: projectString(row.output_price),
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
