import { channelPresets } from './channel-presets'

describe('channelPresets', () => {
  it('provides the approved built-in connection defaults and editable Custom preset', () => {
    expect(channelPresets).toEqual([
      {
        id: 'openai',
        labelKey: 'import.presets.openai',
        upstream_url: 'https://api.openai.com',
        protocols: ['openai-chat-completions', 'openai-responses'],
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
    ])
  })
})
