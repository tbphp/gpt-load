import type { CreateAccessKeyRequest, UpdateAccessKeyRequest } from '@/api/control/access-keys'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { AccessKeyDto, AccessKeyFiltersDto } from '@/api/control/types'

export interface AccessKeyDraft {
  name: string
  status: AccessKeyDto['status']
  filters: AccessKeyFiltersDto
  rpm_limit: number
}

function unique<T>(values: T[]): T[] {
  return [...new Set(values)]
}

export function normalizeAccessKeyFilters(filters: AccessKeyFiltersDto): AccessKeyFiltersDto {
  return {
    groups: unique(filters.groups),
    protocols: unique(filters.protocols),
    models: unique(filters.models.map((value) => value.trim()).filter(Boolean)),
  }
}

export function createAccessKeyDraft(accessKey?: AccessKeyDto | null): AccessKeyDraft {
  return {
    name: accessKey?.name ?? '',
    status: accessKey?.status ?? 'active',
    filters: normalizeAccessKeyFilters(
      accessKey?.filters ?? { groups: [], protocols: [], models: [] },
    ),
    rpm_limit: accessKey?.rpm_limit ?? 0,
  }
}

export function isAccessKeyDraftValid(draft: AccessKeyDraft, base?: AccessKeyDto | null): boolean {
  const baseRetainsReserved = base?.filters.protocols.includes('openai-response') ?? false
  const protocolsValid = draft.filters.protocols.every(
    (protocol) =>
      enabledDataProtocols.some((enabled) => enabled === protocol) ||
      (protocol === 'openai-response' && baseRetainsReserved),
  )
  return (
    draft.name.trim().length > 0 &&
    Number.isSafeInteger(draft.rpm_limit) &&
    draft.rpm_limit >= 0 &&
    protocolsValid
  )
}

export function buildCreateAccessKeyInput(draft: AccessKeyDraft): CreateAccessKeyRequest {
  return {
    name: draft.name.trim(),
    filters: normalizeAccessKeyFilters(draft.filters),
    rpm_limit: draft.rpm_limit,
  }
}

function compareStrings(left: string, right: string): number {
  if (left < right) return -1
  if (left > right) return 1
  return 0
}

function canonicalFilters(filters: AccessKeyFiltersDto): AccessKeyFiltersDto {
  const normalized = normalizeAccessKeyFilters(filters)
  return {
    groups: [...normalized.groups].sort((left, right) => left - right),
    protocols: [...normalized.protocols].sort(compareStrings),
    models: [...normalized.models].sort(compareStrings),
  }
}

function equalFilters(left: AccessKeyFiltersDto, right: AccessKeyFiltersDto): boolean {
  return JSON.stringify(canonicalFilters(left)) === JSON.stringify(canonicalFilters(right))
}

export function buildAccessKeyUpdatePatch(
  base: AccessKeyDto,
  draft: AccessKeyDraft,
): UpdateAccessKeyRequest {
  const patch: UpdateAccessKeyRequest = {}
  const name = draft.name.trim()
  const filters = normalizeAccessKeyFilters(draft.filters)
  if (name !== base.name) patch.name = name
  if (draft.status !== base.status) patch.status = draft.status
  if (!equalFilters(filters, base.filters)) patch.filters = filters
  if (draft.rpm_limit !== base.rpm_limit) patch.rpm_limit = draft.rpm_limit
  return patch
}
