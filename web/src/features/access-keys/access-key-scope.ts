import { enabledDataProtocols } from '@/api/control/protocols'
import type { AccessKeyFiltersDto } from '@/api/control/types'

export type AccessKeyScopeMode = 'all' | 'restricted'
export type AccessKeyScopeDimension = keyof AccessKeyFiltersDto
export type AccessKeyScopeModes = Record<AccessKeyScopeDimension, AccessKeyScopeMode>
export type GroupCatalogState = 'loading' | 'ready' | 'stale' | 'error'

export interface AccessKeyScopeValidation {
  base: AccessKeyFiltersDto | null
  filters: AccessKeyFiltersDto
  modes: AccessKeyScopeModes
  groupCatalog: {
    state: GroupCatalogState
    ids: number[]
  }
}

export function createAccessKeyScopeModes(filters: AccessKeyFiltersDto): AccessKeyScopeModes {
  return {
    groups: filters.groups.length === 0 ? 'all' : 'restricted',
    protocols: filters.protocols.length === 0 ? 'all' : 'restricted',
    models: filters.models.length === 0 ? 'all' : 'restricted',
  }
}

export function materializeAccessKeyFilters(
  filters: AccessKeyFiltersDto,
  modes: AccessKeyScopeModes,
): AccessKeyFiltersDto {
  return {
    groups: modes.groups === 'all' ? [] : [...new Set(filters.groups)],
    protocols: modes.protocols === 'all' ? [] : [...new Set(filters.protocols)],
    models:
      modes.models === 'all'
        ? []
        : [...new Set(filters.models.map((model) => model.trim()).filter(Boolean))],
  }
}

function sameSet<T>(left: readonly T[], right: readonly T[]): boolean {
  if (left.length !== right.length) return false
  const rightSet = new Set(right)
  return left.every((value) => rightSet.has(value))
}

function filtersEqual(left: AccessKeyFiltersDto, right: AccessKeyFiltersDto): boolean {
  return (
    sameSet(left.groups, right.groups) &&
    sameSet(left.protocols, right.protocols) &&
    sameSet(left.models, right.models)
  )
}

function isSubset<T>(values: readonly T[], allowed: readonly T[]): boolean {
  const allowedSet = new Set(allowed)
  return values.every((value) => allowedSet.has(value))
}

export function validateAccessKeyScope(input: AccessKeyScopeValidation): boolean {
  const effective = materializeAccessKeyFilters(input.filters, input.modes)
  for (const dimension of ['groups', 'protocols', 'models'] as const) {
    if (input.modes[dimension] === 'restricted' && effective[dimension].length === 0) {
      return false
    }
  }

  if (input.groupCatalog.state === 'loading' || input.groupCatalog.state === 'error') {
    if (!input.base) return false
    return filtersEqual(effective, input.base)
  }

  if (input.groupCatalog.state === 'stale') {
    if (!input.base) return false
    const baseModes = createAccessKeyScopeModes(input.base)
    return (
      (baseModes.groups === 'all'
        ? input.modes.groups === 'all'
        : input.modes.groups === 'restricted' && isSubset(effective.groups, input.base.groups)) &&
      (baseModes.protocols === 'all'
        ? input.modes.protocols === 'all'
        : input.modes.protocols === 'restricted' &&
          isSubset(effective.protocols, input.base.protocols)) &&
      (baseModes.models === 'all'
        ? input.modes.models === 'all'
        : input.modes.models === 'restricted' && isSubset(effective.models, input.base.models))
    )
  }

  const baseGroups = input.base?.groups ?? []
  const currentGroups = new Set(input.groupCatalog.ids)
  if (
    effective.groups.some(
      (id) => !currentGroups.has(id) && !baseGroups.some((baseID) => baseID === id),
    )
  ) {
    return false
  }

  const baseProtocols = input.base?.protocols ?? []
  if (
    effective.protocols.some(
      (protocol) =>
        !enabledDataProtocols.some((enabled) => enabled === protocol) &&
        !baseProtocols.some((baseProtocol) => baseProtocol === protocol),
    )
  ) {
    return false
  }
  return true
}
