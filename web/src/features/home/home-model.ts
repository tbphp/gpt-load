import type { AccessKeyDto, GroupSummary, KeyCounts } from '@/api/control/types'

export function isGroupServiceable(group: GroupSummary, counts?: KeyCounts): boolean | undefined {
  if (!counts) return undefined
  return group.enabled && group.models.length > 0 && counts.available > 0
}

export function selectInitialAccessKey(keys: AccessKeyDto[]): number | undefined {
  return keys.find((key) => key.status === 'active')?.id ?? keys[0]?.id
}

export function isLoopbackHostname(hostname: string): boolean {
  const normalized = hostname.toLowerCase()
  return (
    normalized === 'localhost' ||
    normalized.endsWith('.localhost') ||
    normalized === '127.0.0.1' ||
    normalized === '::1' ||
    normalized === '[::1]'
  )
}

export function normalizeUpstreamHost(upstreamUrl: string): string {
  try {
    return new URL(upstreamUrl).host
  } catch {
    return upstreamUrl
  }
}
