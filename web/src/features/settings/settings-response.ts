import type { SettingsDto } from '@/api/control/settings'

import { rebaseSettingsDraft, type SettingsDraft, type SettingsSection } from './settings-patch'

export type SettingsMutationDecision =
  | {
      kind: 'apply'
      source: 'response' | 'cache'
      settings: SettingsDto
      draft: SettingsDraft
    }
  | { kind: 'refetch' }

export function chooseSettingsMutationResult(
  response: SettingsDto,
  cached: SettingsDto | undefined,
  base: SettingsDto,
  draft: SettingsDraft,
  section: SettingsSection,
): SettingsMutationDecision {
  if (cached === undefined) return { kind: 'refetch' }

  const source = response.revision >= cached.revision ? 'response' : 'cache'
  const settings = source === 'response' ? response : cached

  return {
    kind: 'apply',
    source,
    settings,
    draft: rebaseSettingsDraft(base, draft, settings, section),
  }
}
