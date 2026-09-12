import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import type { AuthorizationMethod, ProxyOverride, GroupCreateResult } from './group-create'
import { integer, list, oneOf, record, text } from './response'

export const stageStatuses = [
  'pending_authorization',
  'exchanging',
  'ready',
  'consumed',
  'failed',
  'cancelled',
  'expired',
  'outcome_unknown',
] as const
export interface CredentialStage {
  id: string
  status: (typeof stageStatuses)[number]
  method?: AuthorizationMethod
  authorizationURL?: string
  redirectURI?: string
  userCode?: string
  nextPollAt?: number
  email?: string
  expiresAt: number
  errorCode?: string
}
export interface CredentialImportItem {
  index: number
  fileIndex: number
  importID?: string
  format?: 'cpa' | 'codex' | 'claude-code' | 'sub2api'
  status: 'ready' | 'skipped' | 'failed'
  stage?: CredentialStage
  errorCode?: string
}
function stageID(value: unknown): string {
  const id = text(value)
  if (!/^[a-zA-Z0-9_-]{1,100}$/u.test(id)) throw new InvalidResponseError()
  return id
}
function httpURL(value: unknown): string {
  const raw = text(value)
  try {
    const url = new URL(raw)
    if (
      !['http:', 'https:'].includes(url.protocol) ||
      !url.hostname ||
      url.username ||
      url.password
    )
      throw new Error()
  } catch {
    throw new InvalidResponseError()
  }
  return raw
}
function errorCode(value: unknown): string | undefined {
  if (value === undefined) return undefined
  const code = text(value)
  if (!/^[a-z0-9_]{1,64}$/u.test(code)) throw new InvalidResponseError()
  return code
}
function readStage(value: unknown): CredentialStage {
  const data = record(value)
  const account = record(data.account)
  return {
    id: stageID(data.stage_id),
    status: oneOf(data.status, stageStatuses),
    expiresAt: integer(data.expires_at_ms),
    method:
      data.authorization_method === undefined
        ? undefined
        : oneOf(data.authorization_method, [
            'browser_oauth',
            'device_oauth',
            'oauth_file',
          ] as const),
    authorizationURL:
      data.authorization_url === undefined ? undefined : httpURL(data.authorization_url),
    redirectURI: data.redirect_uri === undefined ? undefined : httpURL(data.redirect_uri),
    userCode: data.user_code === undefined ? undefined : text(data.user_code),
    nextPollAt: data.next_poll_at_ms === undefined ? undefined : integer(data.next_poll_at_ms),
    email: account.email_mask === undefined ? undefined : text(account.email_mask),
    errorCode: errorCode(data.error_code),
  }
}
function requestedStage(value: unknown, id: string): CredentialStage {
  const result = readStage(value)
  if (result.id !== id) throw new InvalidResponseError()
  return result
}
export async function beginAuthorization(
  client: ApiClient,
  channelID: string,
  proxy: ProxyOverride | undefined,
  signal: AbortSignal,
  groupID?: number,
): Promise<CredentialStage> {
  return readStage(
    await client.request('/api/credential-stages/authorizations', {
      method: 'POST',
      json: {
        channel_id: channelID,
        ...(groupID ? { group_id: groupID } : proxy ? { proxy } : {}),
      },
      signal,
    }),
  )
}
export async function readCredentialStage(
  client: ApiClient,
  id: string,
  signal: AbortSignal,
): Promise<CredentialStage> {
  return requestedStage(
    await client.request(`/api/credential-stages/${stageID(id)}`, { signal }),
    id,
  )
}
export async function checkDeviceAuthorization(
  client: ApiClient,
  id: string,
  signal: AbortSignal,
): Promise<CredentialStage> {
  return requestedStage(
    await client.request(`/api/credential-stages/${stageID(id)}/device-poll`, {
      method: 'POST',
      signal,
    }),
    id,
  )
}
export async function completeAuthorization(
  client: ApiClient,
  id: string,
  callbackURL: string,
  signal: AbortSignal,
): Promise<CredentialStage> {
  return requestedStage(
    await client.request(`/api/credential-stages/${stageID(id)}/oauth-callback`, {
      method: 'POST',
      json: { callback_url: callbackURL },
      signal,
    }),
    id,
  )
}
export async function cancelCredentialStage(
  client: ApiClient,
  id: string,
  signal: AbortSignal,
): Promise<void> {
  await client.request(`/api/credential-stages/${stageID(id)}`, { method: 'DELETE', signal })
}
export async function importCredentialFiles(
  client: ApiClient,
  channelID: string,
  files: File[],
  proxy: ProxyOverride | undefined,
  preparedIDs: string[],
  signal: AbortSignal,
  groupID?: number,
): Promise<CredentialImportItem[]> {
  const form = new FormData()
  form.set('channel_id', channelID)
  if (groupID) form.set('group_id', String(groupID))
  else if (proxy) form.set('proxy', JSON.stringify(proxy))
  if (preparedIDs.length) form.set('prepared_import_ids', JSON.stringify(preparedIDs))
  for (const file of files) form.append('file', file, file.name)
  const data = record(
    await client.request('/api/credential-stages/import-batch', {
      method: 'POST',
      body: form,
      signal,
    }),
  )
  const items = list(data.items).map((raw): CredentialImportItem => {
    const item = record(raw)
    const status = oneOf(item.status, ['ready', 'skipped', 'failed'] as const)
    const stage = item.stage === undefined ? undefined : readStage(item.stage)
    const code = errorCode(item.error_code)
    const importID = item.import_id === undefined ? undefined : text(item.import_id)
    const fileIndex = integer(item.file_index, 1)
    if (
      fileIndex > files.length ||
      (importID !== undefined && !/^[a-f0-9]{64}$/u.test(importID)) ||
      (status === 'ready' &&
        (stage?.status !== 'ready' || code !== undefined || item.channel_id !== channelID)) ||
      (status !== 'ready' && (stage !== undefined || code === undefined))
    )
      throw new InvalidResponseError()
    return {
      index: integer(item.index, 1),
      fileIndex,
      status,
      stage,
      errorCode: code,
      importID,
      format:
        item.format === undefined
          ? undefined
          : oneOf(item.format, ['cpa', 'codex', 'claude-code', 'sub2api'] as const),
    }
  })
  if (
    !items.length ||
    new Set(items.map((item) => `${item.fileIndex}:${item.index}`)).size !== items.length
  )
    throw new InvalidResponseError()
  return items
}
export async function connectCredentialStages(
  client: ApiClient,
  group: { id: number; name: string },
  ids: string[],
  key: string,
  signal: AbortSignal,
): Promise<GroupCreateResult> {
  const result = record(
    await client.request(`/api/groups/${group.id}/credentials/connect`, {
      method: 'POST',
      json: { staged_credential_ids: ids.map(stageID) },
      headers: { 'Idempotency-Key': key },
      signal,
    }),
  )
  if (integer(result.group_id, 1) !== group.id) throw new InvalidResponseError()
  return {
    ...group,
    added: integer(result.credentials_added),
    duplicated: integer(result.credentials_duplicated),
  }
}
