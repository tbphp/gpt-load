import type { AccessKeyDto, AccessProtocol, GroupSummary } from '@/api/control/types'

export interface AccessKeyPresentation {
  id: number
  name: string
  maskedKey: string
  status: AccessKeyDto['status']
  scopeRows: ReadonlyArray<{ label: string; value: string }>
  scopeSummary: string
  rpm: string
  createdAt: number
  updatedAt: number
}

export interface AccessKeyPresenterLabels {
  groups: string
  protocols: string
  models: string
  allGroups: string
  allProtocols: string
  allModels: string
  unlimited: string
}

export interface AccessKeyPresenterOptions {
  locale: string
  labels: AccessKeyPresenterLabels
  protocolLabel(protocol: AccessProtocol): string
}

export function presentAccessKey(
  accessKey: AccessKeyDto,
  groups: readonly GroupSummary[],
  options: AccessKeyPresenterOptions,
): AccessKeyPresentation {
  const groupNames = new Map(groups.map((group) => [group.id, group.name]))
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
    scopeSummary: scopeRows.map((row) => `${row.label}: ${row.value}`).join(' · '),
    rpm:
      accessKey.rpm_limit === 0
        ? options.labels.unlimited
        : new Intl.NumberFormat(options.locale).format(accessKey.rpm_limit),
    createdAt: accessKey.created_at_ms,
    updatedAt: accessKey.updated_at_ms,
  }
}
