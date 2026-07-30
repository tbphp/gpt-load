import type { GroupSummary } from '@/api/control/types'

import { accessKeyProtocolOptions, buildAccessKeyModelOptions } from './access-key-options'

const groups: GroupSummary[] = [
  {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai-chat-completions'],
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
  it('offers all enabled protocols in canonical order for create and edit', () => {
    const expected = ['openai-chat-completions', 'openai-responses', 'anthropic', 'gemini']
    expect(accessKeyProtocolOptions()).toEqual(expected)
    expect(accessKeyProtocolOptions()).toEqual([
      'openai-chat-completions',
      'openai-responses',
      'anthropic',
      'gemini',
    ])
  })

  it('deduplicates known model IDs/aliases and preserves existing or free-entry values', () => {
    expect(buildAccessKeyModelOptions(groups, ['legacy-free', 'gpt-4.1', ' legacy-free '])).toEqual(
      ['gpt-4.1', 'public-gpt', 'shared', 'claude-public', 'legacy-free'],
    )
  })
})
