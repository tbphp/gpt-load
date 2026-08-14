import type { ApiClient } from '@/api/client'
import type { CredentialItemDto } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'

import { projectCredentialItem } from './credentials'
import {
  assertNoSecretLikeFields,
  projectEpochMilliseconds,
  projectEnum,
  projectHTTPURL,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type CredentialStageStatus =
  | 'pending_authorization'
  | 'exchanging'
  | 'ready'
  | 'consumed'
  | 'failed'
  | 'cancelled'
  | 'expired'
  | 'outcome_unknown'

export interface CredentialStageAccount {
  email_mask?: string
  expires_at_ms?: number
  last_refresh_at_ms?: number
}

export interface CredentialStage {
  stage_id: string
  status: CredentialStageStatus
  authorization_url?: string
  account: CredentialStageAccount
  expires_at_ms: number
  error_code?: string
}

export interface CredentialConnectResult {
  group_id: number
  credentials_added: number
  credentials_duplicated: number
}

const stageFields = [
  'stage_id',
  'status',
  'authorization_url',
  'account',
  'expires_at_ms',
  'error_code',
] as const
const accountFields = ['email_mask', 'expires_at_ms', 'last_refresh_at_ms'] as const
const stageStatuses = [
  'pending_authorization',
  'exchanging',
  'ready',
  'consumed',
  'failed',
  'cancelled',
  'expired',
  'outcome_unknown',
] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectStageID(value: unknown): string {
  const id = projectString(value)
  if (!/^[a-zA-Z0-9_-]{1,100}$/u.test(id)) invalidResponse()
  return id
}

function projectAccount(value: unknown): CredentialStageAccount {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, accountFields)
  const emailMask = record.email_mask === undefined ? undefined : projectString(record.email_mask)
  if (emailMask !== undefined && (emailMask.length > 320 || /\s/u.test(emailMask))) {
    invalidResponse()
  }
  return {
    ...(emailMask === undefined ? {} : { email_mask: emailMask }),
    ...(record.expires_at_ms === undefined
      ? {}
      : { expires_at_ms: projectEpochMilliseconds(record.expires_at_ms) }),
    ...(record.last_refresh_at_ms === undefined
      ? {}
      : { last_refresh_at_ms: projectEpochMilliseconds(record.last_refresh_at_ms) }),
  }
}

function projectInternalErrorCode(value: unknown): string {
  const code = projectString(value)
  if (!/^[a-z0-9_]{1,64}$/u.test(code)) invalidResponse()
  return code
}

export function projectCredentialStage(value: unknown): CredentialStage {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, stageFields)
  const authorizationURL =
    record.authorization_url === undefined ? undefined : projectHTTPURL(record.authorization_url)
  return {
    stage_id: projectStageID(record.stage_id),
    status: projectEnum(record.status, stageStatuses),
    ...(authorizationURL === undefined ? {} : { authorization_url: authorizationURL }),
    account: projectAccount(record.account),
    expires_at_ms: projectEpochMilliseconds(record.expires_at_ms),
    ...(record.error_code === undefined
      ? {}
      : { error_code: projectInternalErrorCode(record.error_code) }),
  }
}

function projectConnectResult(value: unknown): CredentialConnectResult {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['group_id', 'credentials_added', 'credentials_duplicated'])
  return {
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    credentials_added: projectSafeInteger(record.credentials_added, { minimum: 0 }),
    credentials_duplicated: projectSafeInteger(record.credentials_duplicated, { minimum: 0 }),
  }
}

export async function beginCredentialAuthorization(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<CredentialStage> {
  return projectCredentialStage(
    await client.request('/api/credential-stages/authorizations', {
      method: 'POST',
      json: { channel_id: 'codex' },
      signal,
    }),
  )
}

export async function completeCredentialAuthorization(
  client: ApiClient,
  stageID: string,
  callbackURL: string,
  signal?: AbortSignal,
): Promise<CredentialStage> {
  const id = projectStageID(stageID)
  return projectCredentialStage(
    await client.request(`/api/credential-stages/${id}/oauth-callback`, {
      method: 'POST',
      json: { callback_url: callbackURL },
      signal,
    }),
  )
}

export async function importCredentialStage(
  client: ApiClient,
  file: File,
  signal?: AbortSignal,
): Promise<CredentialStage> {
  const body = new FormData()
  body.set('file', file, file.name)
  return projectCredentialStage(
    await client.request('/api/credential-stages/import', { method: 'POST', body, signal }),
  )
}

export async function getCredentialStage(
  client: ApiClient,
  stageID: string,
  signal?: AbortSignal,
): Promise<CredentialStage> {
  const id = projectStageID(stageID)
  return projectCredentialStage(
    await client.request(`/api/credential-stages/${id}`, { method: 'GET', signal }),
  )
}

export async function cancelCredentialStage(
  client: ApiClient,
  stageID: string,
  signal?: AbortSignal,
): Promise<void> {
  const id = projectStageID(stageID)
  await client.request(`/api/credential-stages/${id}`, { method: 'DELETE', signal })
}

export async function connectGroupCredentials(
  client: ApiClient,
  groupID: number,
  stageIDs: string[],
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<CredentialConnectResult> {
  const result = projectConnectResult(
    await client.request(`/api/groups/${groupID}/credentials/connect`, {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      json: { staged_credential_ids: stageIDs.map(projectStageID) },
      signal,
    }),
  )
  if (result.group_id !== groupID) invalidResponse()
  return result
}

export async function reauthorizeGroupCredential(
  client: ApiClient,
  groupID: number,
  credentialID: number,
  stageID: string,
  expectedSecretVersion: number,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<CredentialItemDto> {
  return projectCredentialItem(
    await client.request(`/api/groups/${groupID}/credentials/${credentialID}/reauthorize`, {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      json: {
        stage_id: projectStageID(stageID),
        expected_secret_version: projectSafeInteger(expectedSecretVersion, { minimum: 1 }),
      },
      signal,
    }),
  )
}
