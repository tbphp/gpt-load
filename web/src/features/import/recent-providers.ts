import type { GroupCollectionItemDto, GroupProtocol } from '@/api/control/types'

/**
 * A one-click "use this again" entry derived from an existing Group, so
 * repeat relay/self-hosted connections that will never appear in any
 * provider directory still have a fast path back into the import form.
 */
export interface RecentProviderEntry {
  groupId: number
  groupName: string
  providerId: string | null
  upstreamUrl: string
  protocols: GroupProtocol[]
  host: string
}

const RECENT_PROVIDER_LIMIT = 5

/**
 * Derives up to RECENT_PROVIDER_LIMIT recently created Groups with distinct
 * upstream URLs, preserving the newest-first order the caller is expected to
 * request (collection filters sorted by `created`).
 */
export function deriveRecentProviders(
  items: readonly GroupCollectionItemDto[],
): RecentProviderEntry[] {
  const seenUpstreamUrls = new Set<string>()
  const entries: RecentProviderEntry[] = []
  for (const item of items) {
    if (seenUpstreamUrls.has(item.upstream_url)) continue
    seenUpstreamUrls.add(item.upstream_url)
    entries.push({
      groupId: item.id,
      groupName: item.name,
      providerId: item.provider_id,
      upstreamUrl: item.upstream_url,
      protocols: item.protocols,
      host: extractHost(item.upstream_url),
    })
    if (entries.length >= RECENT_PROVIDER_LIMIT) break
  }
  return entries
}

function extractHost(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}
