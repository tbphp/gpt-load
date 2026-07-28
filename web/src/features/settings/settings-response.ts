import type { RuntimeSettingKey } from '@/api/control/settings'
import type { SettingsResource } from '@/app/resources/settings'

import {
  draftFieldIdentity,
  rebaseSettingsDraft,
  sameSettingsFieldIdentity,
  settingsFieldIdentity,
  settingsSectionKeys,
  type SettingsDraft,
  type SettingsFieldIdentity,
  type SettingsSection,
} from './settings-patch'

export type SettingsMutationDecision =
  | {
      kind: 'apply'
      resource: SettingsResource
      draft: SettingsDraft
    }
  | { kind: 'refetch' }

export interface SettingsMergeConflict {
  key: RuntimeSettingKey
  mine: SettingsFieldIdentity
  latest: SettingsFieldIdentity
}

export interface SettingsMergeResult {
  resource: SettingsResource
  draft: SettingsDraft
  conflicts: SettingsMergeConflict[]
}

export interface SettingsReconciliationResult extends SettingsMergeResult {
  kind: 'confirmed' | 'conflict' | 'indeterminate'
}

export function chooseSettingsMutationResult(
  response: SettingsResource,
  cached: SettingsResource | undefined,
  base: SettingsResource,
  draft: SettingsDraft,
  section: SettingsSection,
): SettingsMutationDecision {
  if (cached === undefined) return { kind: 'refetch' }

  if (
    cached.settings_etag !== response.settings_etag &&
    cached.settings_etag !== base.settings_etag
  ) {
    return { kind: 'refetch' }
  }

  return {
    kind: 'apply',
    resource: response,
    draft: rebaseSettingsDraft(base.settings, draft, response.settings, section),
  }
}

export function mergeSettingsConflict(
  base: SettingsResource,
  draft: SettingsDraft,
  latest: SettingsResource,
  section: SettingsSection,
): SettingsMergeResult {
  const rebased = rebaseSettingsDraft(base.settings, draft, latest.settings, section)
  const conflicts: SettingsMergeConflict[] = []

  for (const key of settingsSectionKeys(section)) {
    const baseIdentity = settingsFieldIdentity(base.settings, key)
    const mineIdentity = draftFieldIdentity(draft, key)
    const latestIdentity = settingsFieldIdentity(latest.settings, key)
    const mineChanged = !sameSettingsFieldIdentity(mineIdentity, baseIdentity)
    const latestChanged = !sameSettingsFieldIdentity(latestIdentity, baseIdentity)

    if (mineChanged && latestChanged && !sameSettingsFieldIdentity(mineIdentity, latestIdentity)) {
      conflicts.push({ key, mine: mineIdentity, latest: latestIdentity })
    }
  }

  return {
    resource: latest,
    draft: rebased,
    conflicts,
  }
}

export function reconcileSettingsMutation(
  base: SettingsResource,
  draft: SettingsDraft,
  latest: SettingsResource,
  section: SettingsSection,
): SettingsReconciliationResult {
  const merged = mergeSettingsConflict(base, draft, latest, section)
  if (merged.conflicts.length > 0) {
    return { kind: 'conflict', ...merged }
  }

  const changedKeys = settingsSectionKeys(section).filter(
    (key) =>
      !sameSettingsFieldIdentity(
        draftFieldIdentity(draft, key),
        settingsFieldIdentity(base.settings, key),
      ),
  )
  const everyIntendedFieldApplied =
    changedKeys.length > 0 &&
    changedKeys.every((key) =>
      sameSettingsFieldIdentity(
        draftFieldIdentity(draft, key),
        settingsFieldIdentity(latest.settings, key),
      ),
    )

  return {
    kind: everyIntendedFieldApplied ? 'confirmed' : 'indeterminate',
    ...merged,
  }
}
