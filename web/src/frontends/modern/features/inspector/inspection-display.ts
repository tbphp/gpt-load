import type { Inspection, InspectionGroup } from '@modern/api/inspector'

export function groupWeight(group: InspectionGroup): number {
  return group.credentials.reduce(
    (total, credential) => total + (credential.available ? credential.effectiveWeight : 0),
    0,
  )
}
// 同组多个模型目标共享凭据，每份凭据权重只累计一次。
export function groupsWeight(groups: readonly InspectionGroup[]): number {
  const weights = new Map<number, number>()
  for (const group of groups) {
    for (const credential of group.credentials) {
      if (credential.available) weights.set(credential.id, credential.effectiveWeight)
    }
  }
  return [...weights.values()].reduce((total, weight) => total + weight, 0)
}
export function activeGroups(result: Inspection): InspectionGroup[] {
  if (!result.routable) return []
  const available = result.groups.filter((group) => group.included && group.routable)
  if (result.strategy === 'weighted_mix') return available
  const mode = available.some((group) => group.mode === 'native') ? 'native' : 'converted'
  return available.filter((group) => group.mode === mode)
}
export function reasonLabel(reason: string | null, t: (key: string) => string): string {
  const known = [
    'access_key_disabled',
    'access_key_expired',
    'protocol_filtered',
    'model_filtered',
    'model_required_by_filter',
    'operation_unsupported',
    'native_route_required',
    'no_route_target',
    'group_disabled',
    'group_filtered',
    'no_available_group',
    'no_credentials',
    'group_weight_zero',
    'credential_disabled',
    'credential_blacklisted',
    'credential_cooldown',
    'model_cooldown',
    'credential_auth_unavailable',
    'credential_weight_zero',
    'credential_not_allowed',
    'no_available_credential',
  ]
  return reason ? (known.includes(reason) ? t('inspector.reasons.' + reason) : reason) : '—'
}
