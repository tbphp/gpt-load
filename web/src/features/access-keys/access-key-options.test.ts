import type { GroupSummary } from '@/api/control/types'

import { accessKeyProtocolOptions, buildAccessKeyModelOptions } from './access-key-options'

const groups: GroupSummary[] = [
  {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai'],
    models: [
      { id: 'gpt-4.1', alias: 'public-gpt' },
      { id: 'shared', alias: '' },
    ],
    enabled: true,
    key_count: 1,
  },
  {
    id: 8,
    name: 'Second',
    upstream_url: 'https://api2.example.com',
    protocols: ['anthropic'],
    models: [{ id: 'shared', alias: 'claude-public' }],
    enabled: true,
    key_count: 1,
  },
]

describe('AccessKey filter options', () => {
  it('offers only enabled protocols for create and ordinary edit', () => {
    expect(accessKeyProtocolOptions()).toEqual(['openai', 'anthropic', 'gemini'])
    expect(accessKeyProtocolOptions(['openai', 'anthropic'])).toEqual([
      'openai',
      'anthropic',
      'gemini',
    ])
  })

  it('appends one reserved option only when the editing base historically contains it', () => {
    expect(accessKeyProtocolOptions(['openai-response', 'openai-response'])).toEqual([
      'openai',
      'anthropic',
      'gemini',
      'openai-response',
    ])
  })

  it('deduplicates known model IDs/aliases and preserves existing or free-entry values', () => {
    expect(buildAccessKeyModelOptions(groups, ['legacy-free', 'gpt-4.1', ' legacy-free '])).toEqual(
      ['gpt-4.1', 'public-gpt', 'shared', 'claude-public', 'legacy-free'],
    )
  })
})
