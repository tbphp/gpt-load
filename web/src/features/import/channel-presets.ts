import type { GroupProtocol } from '@/api/control/types'

export type ProviderPresetCategory = 'native' | 'openai-compatible'

export interface ProviderPreset {
  id: string
  category: ProviderPresetCategory
  featured: boolean
  mark: string
  nameKey: string
  descriptionKey: string
  upstream_url: string
  protocols: readonly GroupProtocol[]
}

export const providerPresetRegistry = [
  {
    id: 'openai',
    category: 'native',
    featured: true,
    mark: 'OA',
    nameKey: 'import.presets.openai.name',
    descriptionKey: 'import.presets.openai.description',
    upstream_url: 'https://api.openai.com',
    protocols: ['openai-completions', 'openai-responses'],
  },
  {
    id: 'anthropic',
    category: 'native',
    featured: true,
    mark: 'AN',
    nameKey: 'import.presets.anthropic.name',
    descriptionKey: 'import.presets.anthropic.description',
    upstream_url: 'https://api.anthropic.com',
    protocols: ['anthropic'],
  },
  {
    id: 'gemini',
    category: 'native',
    featured: true,
    mark: 'GE',
    nameKey: 'import.presets.gemini.name',
    descriptionKey: 'import.presets.gemini.description',
    upstream_url: 'https://generativelanguage.googleapis.com',
    protocols: ['gemini'],
  },
  {
    id: 'deepseek',
    category: 'openai-compatible',
    featured: false,
    mark: 'DS',
    nameKey: 'import.presets.deepseek.name',
    descriptionKey: 'import.presets.deepseek.description',
    upstream_url: 'https://api.deepseek.com',
    protocols: ['openai-completions'],
  },
  {
    id: 'openrouter',
    category: 'openai-compatible',
    featured: false,
    mark: 'OR',
    nameKey: 'import.presets.openrouter.name',
    descriptionKey: 'import.presets.openrouter.description',
    upstream_url: 'https://openrouter.ai/api',
    protocols: ['openai-completions'],
  },
  {
    id: 'siliconflow',
    category: 'openai-compatible',
    featured: false,
    mark: 'SF',
    nameKey: 'import.presets.siliconflow.name',
    descriptionKey: 'import.presets.siliconflow.description',
    upstream_url: 'https://api.siliconflow.cn',
    protocols: ['openai-completions'],
  },
  {
    id: 'moonshot',
    category: 'openai-compatible',
    featured: false,
    mark: 'MO',
    nameKey: 'import.presets.moonshot.name',
    descriptionKey: 'import.presets.moonshot.description',
    upstream_url: 'https://api.moonshot.ai',
    protocols: ['openai-completions'],
  },
] as const satisfies readonly ProviderPreset[]

export type RegisteredProviderPreset = (typeof providerPresetRegistry)[number]
export type ProviderPresetID = RegisteredProviderPreset['id'] | 'custom'

export const featuredProviderPresets = providerPresetRegistry.filter(({ featured }) => featured)
export const catalogProviderPresets = providerPresetRegistry.filter(({ featured }) => !featured)

export function isProviderPresetID(value: unknown): value is ProviderPresetID {
  return (
    value === 'custom' ||
    (typeof value === 'string' && providerPresetRegistry.some(({ id }) => id === value))
  )
}

export function findProviderPreset(id: ProviderPresetID): RegisteredProviderPreset | undefined {
  return providerPresetRegistry.find((preset) => preset.id === id)
}
