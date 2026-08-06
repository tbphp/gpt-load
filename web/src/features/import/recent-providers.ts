import type { GroupCollectionItemDto } from '@/api/control/types'

/**
 * Provider IDs used by the newest Groups. The caller resolves these IDs
 * against the Models.dev catalog before rendering the provider directory.
 */
const RECENT_PROVIDER_LIMIT = 5
// Official providers are already rendered in the featured preset picker.
const OFFICIAL_PROVIDER_IDS = new Set(['openai', 'anthropic', 'google'])

/**
 * Derives up to RECENT_PROVIDER_LIMIT distinct provider IDs from recently
 * created Groups, preserving the newest-first order returned by the Group
 * collection query.
 */
export function deriveRecentProviderIDs(items: readonly GroupCollectionItemDto[]): string[] {
  const seenProviderIDs = new Set<string>()
  const providerIDs: string[] = []
  for (const item of items) {
    const providerID = item.provider_id?.trim()
    // Custom connections have no provider_id and are not Models.dev entries.
    if (!providerID || OFFICIAL_PROVIDER_IDS.has(providerID) || seenProviderIDs.has(providerID))
      continue
    seenProviderIDs.add(providerID)
    providerIDs.push(providerID)
    if (providerIDs.length >= RECENT_PROVIDER_LIMIT) break
  }
  return providerIDs
}
