import type { RuntimeSettingKey } from '@/app/resources/settings'
import type { SettingsResource } from '@/app/resources/settings'

import {
  draftFieldIdentity,
  rebaseSettingsDraft,
  sameSettingsFieldIdentity,
  settingsFieldIdentity,
  settingsScopeKeys,
  type SettingsDraft,
  type SettingsFieldIdentity,
  type SettingsScope,
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
  scope: SettingsScope,
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
    draft: rebaseSettingsDraft(base.settings, draft, response.settings, scope),
  }
}

export function mergeSettingsConflict(
  base: SettingsResource,
  draft: SettingsDraft,
  latest: SettingsResource,
  scope: SettingsScope,
): SettingsMergeResult {
  const rebased = rebaseSettingsDraft(base.settings, draft, latest.settings, scope)
  const conflicts: SettingsMergeConflict[] = []

  for (const key of settingsScopeKeys(scope)) {
    const baseIdentity = settingsFieldIdentity(base.settings, key)
    const mineIdentity = draftFieldIdentity(draft, key)
    const latestIdentity = settingsFieldIdentity(latest.settings, key)
    const mineChanged = !sameSettingsFieldIdentity(mineIdentity, baseIdentity)
    const latestChanged = !sameSettingsFieldIdentity(latestIdentity, baseIdentity)

    if (
      !latestIdentity.is_read_only &&
      mineChanged &&
      latestChanged &&
      !sameSettingsFieldIdentity(mineIdentity, latestIdentity)
    ) {
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
  scope: SettingsScope,
): SettingsReconciliationResult {
  const merged = mergeSettingsConflict(base, draft, latest, scope)
  if (merged.conflicts.length > 0) {
    return { kind: 'conflict', ...merged }
  }

  const changedKeys = settingsScopeKeys(scope).filter(
    (key) =>
      !sameSettingsFieldIdentity(
        draftFieldIdentity(draft, key),
        settingsFieldIdentity(base.settings, key),
      ),
  )
  const everyIntendedFieldApplied =
    changedKeys.length > 0 &&
    changedKeys.every((key) => {
      const latestIdentity = settingsFieldIdentity(latest.settings, key)
      return (
        latestIdentity.is_read_only ||
        sameSettingsFieldIdentity(draftFieldIdentity(draft, key), latestIdentity)
      )
    })

  return {
    kind: everyIntendedFieldApplied ? 'confirmed' : 'indeterminate',
    ...merged,
  }
}
