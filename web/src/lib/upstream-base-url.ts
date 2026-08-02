import type { GroupProtocol } from '@/api/control/types'

export type UpstreamBaseURLWarning = 'missing-prefix' | 'duplicate-prefix'

const protocolPrefixes: Record<GroupProtocol, string> = {
  'openai-completions': '/v1',
  'openai-responses': '/v1',
  anthropic: '/v1',
  gemini: '/v1beta',
}

export function isValidUpstreamBaseURL(value: string): boolean {
  try {
    const parsed = new URL(value.trim())
    return (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      parsed.hostname !== '' &&
      parsed.username === '' &&
      parsed.password === '' &&
      parsed.hash === ''
    )
  } catch {
    return false
  }
}

export function analyzeUpstreamBaseURL(
  value: string,
  protocols: readonly GroupProtocol[],
): UpstreamBaseURLWarning | null {
  if (!isValidUpstreamBaseURL(value) || protocols.length === 0) return null

  const parsed = new URL(value.trim())
  const segments = parsed.pathname.split('/').filter(Boolean)
  const normalizedSegments = segments.map((segment) => segment.toLowerCase())
  const knownPrefixes = new Set(['/v1', '/v1beta'])
  const prefixCounts = new Map<string, number>()
  for (const segment of normalizedSegments) {
    const prefix = `/${segment}`
    if (!knownPrefixes.has(prefix)) continue
    const count = (prefixCounts.get(prefix) ?? 0) + 1
    prefixCounts.set(prefix, count)
    if (count > 1) {
      return 'duplicate-prefix'
    }
  }

  // DeepSeek's official OpenAI-compatible Base URL is the host root.
  if (parsed.hostname.toLowerCase() === 'api.deepseek.com') return null

  const expectedPrefixes = new Set(protocols.map((protocol) => protocolPrefixes[protocol]))
  if (expectedPrefixes.size !== 1) return null
  const expectedPrefix = [...expectedPrefixes][0].slice(1)
  return normalizedSegments.at(-1) === expectedPrefix ? null : 'missing-prefix'
}
