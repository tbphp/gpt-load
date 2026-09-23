import { boolean, integer, list, record, text } from './response'

export interface AutoPreset {
  id: string
  name: string
  description: string
  model: string
  parameter_overrides: unknown[]
}
export interface AutoEntry {
  id: string
  name: string
  enabled: boolean
  fallback: string
  presets: AutoPreset[]
}
export interface AutoModelConfig {
  enabled: boolean
  model: string
  timeout_seconds: number
  models: AutoEntry[]
}
export const defaultAutoModel = (): AutoModelConfig => ({
  enabled: false,
  model: '',
  timeout_seconds: 2,
  models: [],
})
export function readAutoEntry(value: unknown): AutoEntry {
  const row = record(value)
  return {
    id: text(row.id),
    name: text(row.name),
    enabled: boolean(row.enabled),
    fallback: text(row.fallback),
    presets: list(row.presets).map((value) => {
      const preset = record(value)
      return {
        id: text(preset.id),
        name: text(preset.name),
        description: text(preset.description),
        model: text(preset.model),
        parameter_overrides: list(preset.parameter_overrides),
      }
    }),
  }
}
export function readAutoModel(value: unknown): AutoModelConfig {
  if (value === undefined) return defaultAutoModel()
  const row = record(value)
  return {
    enabled: boolean(row.enabled),
    model: text(row.model),
    timeout_seconds: integer(row.timeout_seconds, 1),
    models: list(row.models).map(readAutoEntry),
  }
}

export type AutoModelDraft = Omit<AutoModelConfig, 'models' | 'timeout_seconds'> & {
  timeout_seconds: string
  models: (Omit<AutoEntry, 'presets'> & {
    presets: (Omit<AutoPreset, 'parameter_overrides'> & { parameter_overrides: string })[]
  })[]
}
export function autoModelDraft(config: AutoModelConfig): AutoModelDraft {
  return {
    ...config,
    timeout_seconds: String(config.timeout_seconds),
    models: config.models.map((entry) => ({
      ...entry,
      enabled: true,
      presets: entry.presets.map((preset) => ({
        ...preset,
        parameter_overrides: JSON.stringify(preset.parameter_overrides, null, 2),
      })),
    })),
  }
}
export function autoModelValue(draft: AutoModelDraft): AutoModelConfig {
  return {
    ...draft,
    timeout_seconds: Number(draft.timeout_seconds),
    models: draft.models.map((entry) => ({
      ...entry,
      enabled: true,
      presets: entry.presets.map((preset) => ({
        ...preset,
        parameter_overrides: JSON.parse(preset.parameter_overrides) as unknown[],
      })),
    })),
  }
}
export function validAutoDraft(draft: AutoModelDraft): boolean {
  try {
    const value = autoModelValue(draft)
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
