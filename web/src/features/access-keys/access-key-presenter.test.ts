import type { AccessKeyDto, GroupSummary } from '@/api/control/types'

import { presentAccessKey } from './access-key-presenter'

const accessKey: AccessKeyDto = {
  id: 42,
  name: `client-${'segment-'.repeat(16)}`,
  masked_key: 'sk-gl-••••••••cafe',
  status: 'active',
  filters: {
    groups: [7, 999],
    protocols: ['openai', 'anthropic'],
    models: ['gpt-real', 'claude-real'],
  },
  rpm_limit: 1200,
  created_at: '2026-07-28T00:00:00Z',
  updated_at: '2026-07-29T00:00:00Z',
}
const groups: GroupSummary[] = [
  {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai'],
    models: [],
    enabled: true,
    key_count: 1,
  },
]

describe('AccessKey presenter', () => {
  it('derives table and card values from one resource without losing dangling scope', () => {
    expect(
      presentAccessKey(accessKey, groups, {
        locale: 'en-US',
        labels: {
          groups: 'Groups',
          protocols: 'Protocols',
          models: 'Models',
          allGroups: 'All Groups',
          allProtocols: 'All protocols',
          allModels: 'All models',
          unlimited: 'Unlimited',
        },
        protocolLabel: (protocol) => {
          if (protocol === 'openai') return 'OpenAI'
          if (protocol === 'anthropic') return 'Anthropic'
          return protocol
        },
      }),
    ).toEqual({
      id: 42,
      name: accessKey.name,
      maskedKey: 'sk-gl-••••••••cafe',
      status: 'active',
      scopeRows: [
        { label: 'Groups', value: 'Primary, #999' },
        { label: 'Protocols', value: 'OpenAI, Anthropic' },
        { label: 'Models', value: 'gpt-real, claude-real' },
      ],
      scopeSummary:
        'Groups: Primary, #999 · Protocols: OpenAI, Anthropic · Models: gpt-real, claude-real',
      rpm: '1,200',
      createdAt: '2026-07-28T00:00:00Z',
      updatedAt: '2026-07-29T00:00:00Z',
    })
  })

  it('uses explicit all/unlimited labels for empty scopes', () => {
    const presentation = presentAccessKey(
      {
        ...accessKey,
        filters: { groups: [], protocols: [], models: [] },
        rpm_limit: 0,
      },
      groups,
      {
        locale: 'en-US',
        labels: {
          groups: 'Groups',
          protocols: 'Protocols',
          models: 'Models',
          allGroups: 'All Groups',
          allProtocols: 'All protocols',
          allModels: 'All models',
          unlimited: 'Unlimited',
        },
        protocolLabel: String,
      },
    )

    expect(presentation.scopeRows.map((row) => row.value)).toEqual([
      'All Groups',
      'All protocols',
      'All models',
    ])
    expect(presentation.rpm).toBe('Unlimited')
  })
})
