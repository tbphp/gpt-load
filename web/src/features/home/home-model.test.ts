import type { AccessKeyDto, GroupSummary, KeyCounts } from '@/api/control/types'

import {
  isGroupServiceable,
  isLoopbackHostname,
  normalizeUpstreamHost,
  selectInitialAccessKey,
} from './home-model'

const group: GroupSummary = {
  id: 1,
  name: 'Example',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai'],
  models: [{ id: 'gpt-real', alias: '' }],
  enabled: true,
  key_count: 1,
}
const counts: KeyCounts = {
  total: 1,
  available: 1,
  cooldown: 0,
  blacklisted: 0,
  disabled: 0,
}
const accessKey = (id: number, status: AccessKeyDto['status']): AccessKeyDto => ({
  id,
  name: `key-${id}`,
  key: `sk-gl-${id}`,
  status,
  filters: { groups: [], protocols: [], models: [] },
  rpm_limit: 0,
})

describe('Home model', () => {
  it.each(['localhost', 'api.localhost', '127.0.0.1', '::1', '[::1]'])(
    'detects loopback hostname %s',
    (hostname) => expect(isLoopbackHostname(hostname)).toBe(true),
  )

  it.each(['localhost.example', '127.0.0.2', '192.168.1.10', 'example.com'])(
    'does not classify network hostname %s as loopback',
    (hostname) => expect(isLoopbackHostname(hostname)).toBe(false),
  )

  it('returns unknown serviceability without health counts', () => {
    expect(isGroupServiceable(group)).toBeUndefined()
  })

  it('requires enabled group, a real model, and an available key', () => {
    expect(isGroupServiceable(group, counts)).toBe(true)
    expect(isGroupServiceable({ ...group, enabled: false }, counts)).toBe(false)
    expect(isGroupServiceable({ ...group, models: [] }, counts)).toBe(false)
    expect(isGroupServiceable(group, { ...counts, available: 0 })).toBe(false)
  })

  it('selects the first active AccessKey in backend order, otherwise the first key', () => {
    expect(
      selectInitialAccessKey([
        accessKey(1, 'disabled'),
        accessKey(2, 'active'),
        accessKey(3, 'active'),
      ]),
    ).toBe(2)
    expect(selectInitialAccessKey([accessKey(4, 'disabled')])).toBe(4)
    expect(selectInitialAccessKey([])).toBeUndefined()
  })

  it('shows only the normalized upstream host', () => {
    expect(normalizeUpstreamHost(group.upstream_url)).toBe('api.example.com')
    expect(normalizeUpstreamHost('not a url')).toBe('not a url')
  })
})
