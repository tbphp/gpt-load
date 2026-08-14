<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialItemDto, CredentialQuotaWindowDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = defineProps<{
  item: CredentialItemDto
  busy: boolean
}>()
const emit = defineEmits<{
  refresh: [item: CredentialItemDto]
  reauthorize: [item: CredentialItemDto]
}>()
const { locale, n, t } = useI18n()
const observation = computed(() => props.item.observation)
const snapshot = computed(() => observation.value?.snapshot)
const quotaWindows = computed(() => snapshot.value?.quota_windows ?? [])
const constrainedModels = computed(() =>
  Array.from(new Set(quotaWindows.value.flatMap((window) => window.model_ids ?? []))),
)
const authErrorKeys: Readonly<Record<string, string>> = {
  refresh_rejected: 'group.credentials.subscription.authError.refreshRejected',
  refresh_identity_changed: 'group.credentials.subscription.authError.identityChanged',
  refresh_outcome_unknown: 'group.credentials.subscription.authError.outcomeUnknown',
  refresh_persist_failed: 'group.credentials.subscription.authError.persistFailed',
  refresh_commit_failed: 'group.credentials.subscription.authError.persistFailed',
  refresh_registry_mismatch: 'group.credentials.subscription.authError.runtimeMismatch',
  refresh_start_failed: 'group.credentials.subscription.authError.refreshStartFailed',
  refresh_state_commit_failed: 'group.credentials.subscription.authError.persistFailed',
}
const observationErrorKeys: Readonly<Record<string, string>> = {
  observation_upstream_failed: 'group.credentials.subscription.observationError.upstreamFailed',
  observation_payload_invalid: 'group.credentials.subscription.observationError.payloadInvalid',
}
const authIssue = computed(() => {
  if (props.item.auth_state === 'ready') return ''
  const key = props.item.auth_error_code ? authErrorKeys[props.item.auth_error_code] : undefined
  return key ? t(key) : t(`group.credentials.subscription.auth.${props.item.auth_state}`)
})

function percent(window: CredentialQuotaWindowDto): number | undefined {
  if (window.utilization !== undefined) return Math.round(window.utilization * 100)
  if (window.used !== undefined && window.limit && window.limit > 0) {
    return Math.max(0, Math.min(100, Math.round((window.used / window.limit) * 100)))
  }
  return undefined
}

function quotaValue(window: CredentialQuotaWindowDto): string {
  const value = percent(window)
  if (value !== undefined) return `${n(value)}%`
  if (window.remaining !== undefined && window.limit !== undefined) {
    return t('group.credentials.subscription.remaining', {
      remaining: n(window.remaining),
      limit: n(window.limit),
    })
  }
  return t('group.credentials.subscription.unknown')
}

function authTone(): 'success' | 'warning' | 'danger' {
  if (props.item.auth_state === 'ready') return 'success'
  if (props.item.auth_state === 'refreshing') return 'warning'
  return 'danger'
}

function observationTone(): 'success' | 'warning' | 'neutral' {
  if (observation.value?.state === 'fresh') return 'success'
  if (observation.value?.state === 'refreshing' || observation.value?.state === 'error') {
    return 'warning'
  }
  return 'neutral'
}

function quotaScope(window: CredentialQuotaWindowDto): string {
  const known: Readonly<Record<string, string>> = {
    account: 'group.credentials.subscription.scope.account',
    global: 'group.credentials.subscription.scope.global',
    model: 'group.credentials.subscription.scope.model',
  }
  const key = known[window.scope]
  return key ? t(key) : window.scope
}

function quotaUnit(window: CredentialQuotaWindowDto): string {
  return window.unit === 'percent' ? t('group.credentials.subscription.unit.percent') : window.unit
}

function observationErrorLabel(code: string | undefined): string {
  if (!code) return '—'
  const key = observationErrorKeys[code]
  return key ? t(key) : code
}
</script>

<template>
  <div class="subscription-credential-details">
    <div class="subscription-credential-details__topline">
      <div class="subscription-credential-details__badges">
        <StatusBadge :tone="authTone()" size="compact">
          {{ t(`group.credentials.subscription.auth.${item.auth_state}`) }}
        </StatusBadge>
        <StatusBadge :status="item.effective_status" size="compact">
          {{ t(`group.credentials.effective.${item.effective_status}`) }}
        </StatusBadge>
        <StatusBadge :tone="observationTone()" size="compact">
          {{
            t(
              `group.credentials.subscription.observationShort.${observation?.state ?? 'unavailable'}`,
            )
          }}
        </StatusBadge>
      </div>
      <div class="subscription-credential-details__actions">
        <AppButton
          variant="secondary"
          size="compact"
          :disabled="busy"
          @click="emit('reauthorize', item)"
        >
          {{ t('group.credentials.subscription.reauthorize') }}
        </AppButton>
        <AppButton size="compact" :busy="busy" @click="emit('refresh', item)">
          {{ t('group.credentials.subscription.sync') }}
        </AppButton>
      </div>
    </div>

    <dl class="subscription-credential-details__identity">
      <div>
        <dt>{{ t('group.credentials.subscription.account') }}</dt>
        <dd>
          <code>{{ item.mask }}</code>
        </dd>
      </div>
      <div>
        <dt>{{ t('group.credentials.subscription.plan') }}</dt>
        <dd>{{ snapshot?.plan_summary.name || t('group.credentials.subscription.unknown') }}</dd>
      </div>
      <div>
        <dt>{{ t('group.credentials.subscription.resetCredits') }}</dt>
        <dd>
          {{
            snapshot?.reset_credits_available === undefined
              ? t('group.credentials.subscription.unknown')
              : n(snapshot.reset_credits_available)
          }}
        </dd>
      </div>
      <div>
        <dt>{{ t('group.credentials.subscription.tokenExpiresAt') }}</dt>
        <dd>
          <AppDateTime
            v-if="item.account.expires_at_ms"
            :instant="item.account.expires_at_ms"
            :locale="locale"
          />
          <span v-else>—</span>
        </dd>
      </div>
      <div>
        <dt>{{ t('group.credentials.subscription.lastTokenRefresh') }}</dt>
        <dd>
          <AppDateTime
            v-if="item.account.last_refresh_at_ms"
            :instant="item.account.last_refresh_at_ms"
            :locale="locale"
          />
          <span v-else>—</span>
        </dd>
      </div>
    </dl>

    <InlineFeedback v-if="authIssue" tone="danger" appearance="ledger">
      {{ authIssue }}
    </InlineFeedback>

    <InlineFeedback
      v-if="observation?.state !== 'fresh'"
      :tone="observation?.state === 'error' ? 'warning' : 'neutral'"
      appearance="ledger"
    >
      {{ t(`group.credentials.subscription.observation.${observation?.state ?? 'unavailable'}`) }}
    </InlineFeedback>

    <section class="subscription-credential-details__section">
      <h3>{{ t('group.credentials.subscription.quotas') }}</h3>
      <p v-if="quotaWindows.length === 0" class="subscription-credential-details__faint">
        {{ t('group.credentials.subscription.noQuota') }}
      </p>
      <div v-else class="subscription-credential-details__quotas">
        <article v-for="window in quotaWindows" :key="window.id">
          <div class="subscription-credential-details__quota-heading">
            <strong>{{ window.label }}</strong>
            <span>{{ quotaValue(window) }}</span>
          </div>
          <div
            v-if="percent(window) !== undefined"
            class="subscription-credential-details__progress"
            role="progressbar"
            :aria-label="window.label"
            :aria-valuenow="percent(window)"
            :aria-valuetext="quotaValue(window)"
            aria-valuemin="0"
            aria-valuemax="100"
          >
            <span
              :class="{ 'is-exhausted': window.state === 'exhausted' }"
              :style="{ width: `${percent(window)}%` }"
            />
          </div>
          <div
            v-else
            class="subscription-credential-details__progress subscription-credential-details__progress--unknown"
            role="img"
            :aria-label="`${window.label}: ${quotaValue(window)}`"
          />
          <div class="subscription-credential-details__quota-meta">
            <span>{{ t(`group.credentials.subscription.quotaState.${window.state}`) }}</span>
            <span>{{ quotaScope(window) }} · {{ quotaUnit(window) }}</span>
            <span v-if="window.reset_at_ms">
              {{ t('group.credentials.subscription.resetAt') }}
              <AppDateTime :instant="window.reset_at_ms" :locale="locale" />
            </span>
          </div>
        </article>
      </div>
    </section>

    <section class="subscription-credential-details__section">
      <h3>{{ t('group.credentials.subscription.modelConstraints') }}</h3>
      <p v-if="constrainedModels.length === 0" class="subscription-credential-details__faint">
        {{ t('group.credentials.subscription.noModelConstraints') }}
      </p>
      <div v-else class="subscription-credential-details__models">
        <code v-for="model in constrainedModels" :key="model">{{ model }}</code>
      </div>
    </section>

    <section class="subscription-credential-details__section">
      <h3>{{ t('group.credentials.subscription.syncInfo') }}</h3>
      <dl class="subscription-credential-details__sync">
        <div>
          <dt>{{ t('group.credentials.subscription.observedAt') }}</dt>
          <dd>
            <AppDateTime
              v-if="observation?.observed_at_ms"
              :instant="observation.observed_at_ms"
              :locale="locale"
            />
            <span v-else>—</span>
          </dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.freshUntil') }}</dt>
          <dd>
            <AppDateTime
              v-if="observation?.fresh_until_ms"
              :instant="observation.fresh_until_ms"
              :locale="locale"
            />
            <span v-else>—</span>
          </dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.lastAttempt') }}</dt>
          <dd>
            <AppDateTime
              v-if="observation?.last_attempt_at_ms"
              :instant="observation.last_attempt_at_ms"
              :locale="locale"
            />
            <span v-else>—</span>
          </dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.nextAllowed') }}</dt>
          <dd>
            <AppDateTime
              v-if="observation?.next_allowed_at_ms"
              :instant="observation.next_allowed_at_ms"
              :locale="locale"
            />
            <span v-else>—</span>
          </dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.observationVersion') }}</dt>
          <dd>{{ observation ? n(observation.observation_version) : '—' }}</dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.lastError') }}</dt>
          <dd>
            <span>{{ observationErrorLabel(observation?.last_error_code) }}</span>
          </dd>
        </div>
      </dl>
    </section>
  </div>
</template>

<style scoped>
.subscription-credential-details {
  display: grid;
  gap: var(--space-4);
}
.subscription-credential-details__topline,
.subscription-credential-details__badges,
.subscription-credential-details__actions,
.subscription-credential-details__quota-heading,
.subscription-credential-details__quota-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.subscription-credential-details__topline,
.subscription-credential-details__quota-heading {
  justify-content: space-between;
}
.subscription-credential-details__badges,
.subscription-credential-details__actions,
.subscription-credential-details__quota-meta {
  flex-wrap: wrap;
}
.subscription-credential-details__identity,
.subscription-credential-details__sync {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
  margin: 0;
}
.subscription-credential-details__identity > div,
.subscription-credential-details__sync > div {
  min-width: 0;
  border-left: 2px solid var(--color-border-control);
  padding-left: var(--space-2);
}
.subscription-credential-details dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-credential-details dd {
  margin: 3px 0 0;
  overflow-wrap: anywhere;
  font-size: var(--text-meta);
  font-weight: 560;
}
.subscription-credential-details code {
  font-family: var(--font-mono);
}
.subscription-credential-details__section {
  display: grid;
  gap: var(--space-2);
}
.subscription-credential-details__section h3,
.subscription-credential-details__faint {
  margin: 0;
}
.subscription-credential-details__section h3 {
  font-size: var(--text-meta);
  font-weight: 650;
}
.subscription-credential-details__faint {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.subscription-credential-details__quotas {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}
.subscription-credential-details__quotas article {
  display: grid;
  gap: 7px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 10px 11px;
}
.subscription-credential-details__quota-heading strong {
  font-size: var(--text-sm);
}
.subscription-credential-details__quota-heading span,
.subscription-credential-details__quota-meta {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}
.subscription-credential-details__progress {
  height: 6px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--color-border-subtle);
}
.subscription-credential-details__progress > span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--color-action);
}
.subscription-credential-details__progress > span.is-exhausted {
  background: var(--color-danger);
}
.subscription-credential-details__progress--unknown {
  background: repeating-linear-gradient(
    135deg,
    var(--color-border-subtle),
    var(--color-border-subtle) 6px,
    var(--color-surface-sunken) 6px,
    var(--color-surface-sunken) 12px
  );
}
.subscription-credential-details__models {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.subscription-credential-details__models code {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 4px 7px;
  font-size: var(--text-label-xs);
}
@media (max-width: 760px) {
  .subscription-credential-details__topline {
    align-items: flex-start;
    flex-direction: column;
  }
  .subscription-credential-details__quotas,
  .subscription-credential-details__identity,
  .subscription-credential-details__sync {
    grid-template-columns: 1fr;
  }
  .subscription-credential-details__actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
