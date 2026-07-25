import type { Protocol } from '@/api/control/types'

export type GroupProtocol = Protocol

export interface ChannelPreset {
  id: 'openai' | 'anthropic' | 'gemini' | 'custom'
  labelKey: string
  upstream_url: string
  protocols: GroupProtocol[]
}

export const channelPresets: ChannelPreset[] = [
  {
    id: 'openai',
    labelKey: 'import.presets.openai',
    upstream_url: 'https://api.openai.com',
    protocols: ['openai'],
  },
  {
    id: 'anthropic',
    labelKey: 'import.presets.anthropic',
    upstream_url: 'https://api.anthropic.com',
    protocols: ['anthropic'],
  },
  {
    id: 'gemini',
    labelKey: 'import.presets.gemini',
    upstream_url: 'https://generativelanguage.googleapis.com',
    protocols: ['gemini'],
  },
  {
    id: 'custom',
    labelKey: 'import.presets.custom',
    upstream_url: '',
    protocols: [],
  },
]
