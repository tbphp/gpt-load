import type {
  AccessKeyCollectionItemDto,
  AccessKeyDto,
  AccessProtocol,
  GroupOptionDto,
} from '@/api/control/types'

export interface AccessKeyPresentation {
  id: number
  name: string
  maskedKey: string
  status: AccessKeyDto['status']
  scopeRows: ReadonlyArray<{ label: string; value: string }>
  limits: string
  quotaExhausted: boolean
  lastRequestAt: number | null
}

export interface AccessKeyPresenterLabels {
  groups: string
  protocols: string
  models: string
  allGroups: string
  allProtocols: string
  allModels: string
  unlimited: string
  costRules(count: number): string
}

export interface AccessKeyPresenterOptions {
  locale: string
  labels: AccessKeyPresenterLabels
  protocolLabel(protocol: AccessProtocol): string
}

function presentAccessKeyWithGroupNames(
  accessKey: AccessKeyCollectionItemDto,
  groupNames: ReadonlyMap<number, string>,
  options: AccessKeyPresenterOptions,
): AccessKeyPresentation {
  const groupValue =
    accessKey.filters.groups.length === 0
      ? options.labels.allGroups
      : accessKey.filters.groups.map((id) => groupNames.get(id) ?? `#${id}`).join(', ')
  const protocolValue =
    accessKey.filters.protocols.length === 0
      ? options.labels.allProtocols
      : accessKey.filters.protocols.map(options.protocolLabel).join(', ')
  const modelValue =
    accessKey.filters.models.length === 0
      ? options.labels.allModels
      : accessKey.filters.models.join(', ')
  const scopeRows = [
    { label: options.labels.groups, value: groupValue },
    { label: options.labels.protocols, value: protocolValue },
    { label: options.labels.models, value: modelValue },
  ] as const

  return {
    id: accessKey.id,
    name: accessKey.name,
    maskedKey: accessKey.masked_key,
    status: accessKey.status,
    scopeRows,
    limits: [
      accessKey.rpm_limit === 0
        ? options.labels.unlimited
        : `${new Intl.NumberFormat(options.locale).format(accessKey.rpm_limit)} RPM`,
      ...(accessKey.cost_limit_rules.length > 0
        ? [options.labels.costRules(accessKey.cost_limit_rules.length)]
        : []),
    ].join(' · '),
    quotaExhausted: accessKey.cost_limit_status?.allowed === false,
    lastRequestAt: accessKey.last_request_at_ms,
  }
}

export function presentAccessKeyCollection(
  accessKeys: readonly AccessKeyCollectionItemDto[],
  groups: readonly GroupOptionDto[],
  options: AccessKeyPresenterOptions,
): AccessKeyPresentation[] {
  const groupNames = new Map(groups.map((group) => [group.id, group.name]))
  return accessKeys.map((accessKey) =>
    presentAccessKeyWithGroupNames(accessKey, groupNames, options),
  )
}
