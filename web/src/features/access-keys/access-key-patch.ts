import type { CreateAccessKeyRequest, UpdateAccessKeyRequest } from '@/api/control/access-keys'
import type { AccessKeyDto, AccessKeyFiltersDto } from '@/api/control/types'

import {
  createAccessKeyScopeModes,
  materializeAccessKeyFilters,
  validateAccessKeyScope,
  type AccessKeyScopeModes,
  type GroupCatalogState,
} from './access-key-scope'

export interface AccessKeyDraft {
  name: string
  status: AccessKeyDto['status']
  filters: AccessKeyFiltersDto
  scopeModes: AccessKeyScopeModes
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
  const filters = normalizeAccessKeyFilters(
    accessKey?.filters ?? { groups: [], protocols: [], models: [] },
  )
  return {
    name: accessKey?.name ?? '',
    status: accessKey?.status ?? 'active',
    filters,
    scopeModes: createAccessKeyScopeModes(filters),
    rpm_limit: accessKey?.rpm_limit ?? 0,
  }
}

export function createAccessKeyDraftFromCreateInput(input: CreateAccessKeyRequest): AccessKeyDraft {
  const filters = normalizeAccessKeyFilters(input.filters)
  return {
    name: input.name,
    status: 'active',
    filters,
    scopeModes: createAccessKeyScopeModes(filters),
    rpm_limit: input.rpm_limit,
  }
}

export function isAccessKeyDraftValid(
  draft: AccessKeyDraft,
  base?: AccessKeyDto | null,
  groupCatalog: { state: GroupCatalogState; ids: number[] } = {
    state: 'ready',
    ids: [...new Set([...(base?.filters.groups ?? []), ...draft.filters.groups])],
  },
): boolean {
  return (
    draft.name.trim().length > 0 &&
    Number.isSafeInteger(draft.rpm_limit) &&
    draft.rpm_limit >= 0 &&
    validateAccessKeyScope({
      base: base?.filters ?? null,
      filters: draft.filters,
      modes: draft.scopeModes,
      groupCatalog,
    })
  )
}

export function buildCreateAccessKeyInput(draft: AccessKeyDraft): CreateAccessKeyRequest {
  return {
    name: draft.name.trim(),
    filters: normalizeAccessKeyFilters(
      materializeAccessKeyFilters(draft.filters, draft.scopeModes),
    ),
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

export function isAccessKeyDraftDirty(draft: AccessKeyDraft, base?: AccessKeyDto | null): boolean {
  const initial = createAccessKeyDraft(base)
  if (
    draft.scopeModes.groups !== initial.scopeModes.groups ||
    draft.scopeModes.protocols !== initial.scopeModes.protocols ||
    draft.scopeModes.models !== initial.scopeModes.models
  ) {
    return true
  }
  if (base) return Object.keys(buildAccessKeyUpdatePatch(base, draft)).length > 0
  return (
    draft.name !== initial.name ||
    draft.status !== initial.status ||
    draft.rpm_limit !== initial.rpm_limit ||
    !equalFilters(draft.filters, initial.filters)
  )
}

export function accessKeyMatchesUpdatePatch(
  accessKey: AccessKeyDto,
  patch: UpdateAccessKeyRequest,
): boolean {
  return (
    (patch.name === undefined || patch.name === accessKey.name) &&
    (patch.status === undefined || patch.status === accessKey.status) &&
    (patch.filters === undefined || equalFilters(patch.filters, accessKey.filters)) &&
    (patch.rpm_limit === undefined || patch.rpm_limit === accessKey.rpm_limit)
  )
}

export function buildAccessKeyUpdatePatch(
  base: AccessKeyDto,
  draft: AccessKeyDraft,
): UpdateAccessKeyRequest {
  const patch: UpdateAccessKeyRequest = {}
  const name = draft.name.trim()
  const filters = normalizeAccessKeyFilters(
    materializeAccessKeyFilters(draft.filters, draft.scopeModes),
  )
  if (name !== base.name) patch.name = name
  if (draft.status !== base.status) patch.status = draft.status
  if (!equalFilters(filters, base.filters)) patch.filters = filters
  if (draft.rpm_limit !== base.rpm_limit) patch.rpm_limit = draft.rpm_limit
  return patch
}
