import { ApiError } from '@/api/errors'

const subscriptionErrorKeys: Readonly<Record<string, string>> = {
  OAUTH_FILE_INVALID: 'common.subscriptionErrors.oauthFileInvalid',
  OAUTH_FILE_TOO_LARGE: 'common.subscriptionErrors.oauthFileTooLarge',
  AUTHORIZATION_UNAVAILABLE: 'common.subscriptionErrors.authorizationUnavailable',
  AUTHORIZATION_STATE_INVALID: 'common.subscriptionErrors.authorizationStateInvalid',
  AUTHORIZATION_EXCHANGE_FAILED: 'common.subscriptionErrors.authorizationExchangeFailed',
  STAGED_CREDENTIAL_NOT_READY: 'common.subscriptionErrors.stageNotReady',
  STAGED_CREDENTIAL_EXPIRED: 'common.subscriptionErrors.stageExpired',
  STAGED_CREDENTIAL_CONSUMED: 'common.subscriptionErrors.stageConsumed',
  STAGED_CREDENTIAL_MISMATCH: 'common.subscriptionErrors.stageMismatch',
  DUPLICATE_CREDENTIAL_IDENTITY: 'common.subscriptionErrors.duplicateAccount',
  CREDENTIAL_REAUTHORIZATION_REQUIRED: 'common.subscriptionErrors.reauthorizationRequired',
  CREDENTIAL_AUTH_OUTCOME_UNKNOWN: 'common.subscriptionErrors.outcomeUnknown',
  CREDENTIAL_REFRESH_TEMPORARILY_UNAVAILABLE:
    'common.subscriptionErrors.refreshTemporarilyUnavailable',
  CREDENTIAL_VERSION_CONFLICT: 'common.subscriptionErrors.versionConflict',
  RESET_CREDIT_UNAVAILABLE: 'common.subscriptionErrors.resetCreditUnavailable',
  RESET_CREDIT_REJECTED: 'common.subscriptionErrors.resetCreditRejected',
  RESET_CREDIT_OUTCOME_UNKNOWN: 'common.subscriptionErrors.resetCreditOutcomeUnknown',
  CONTROL_OPERATION_INCOMPLETE: 'common.subscriptionErrors.operationIncomplete',
  CONTROL_RECOVERY_PENDING: 'common.subscriptionErrors.recoveryPending',
}

export function presentSubscriptionErrorKey(cause: unknown, fallbackKey: string): string {
  if (!(cause instanceof ApiError)) return fallbackKey
  return subscriptionErrorKeys[cause.code] ?? fallbackKey
}
