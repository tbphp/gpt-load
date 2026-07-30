import { normalizeUpstreamHost } from './upstream-host'

describe('normalizeUpstreamHost', () => {
  it('shows only the normalized upstream host', () => {
    expect(normalizeUpstreamHost('https://api.example.com/v1')).toBe('api.example.com')
    expect(normalizeUpstreamHost('not a url')).toBe('not a url')
  })
})
