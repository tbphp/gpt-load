<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialItemDto, CredentialQuotaWindowDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = defineProps<{
  open: boolean
  item: CredentialItemDto | null
  busy: boolean
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  refresh: [item: CredentialItemDto]
  reauthorize: [item: CredentialItemDto]
}>()
const { locale, n, t } = useI18n()
const observation = computed(() => props.item?.observation)
const snapshot = computed(() => observation.value?.snapshot)
const quotaWindows = computed(() => snapshot.value?.quota_windows ?? [])
const constrainedModels = computed(() =>
  Array.from(new Set(quotaWindows.value.flatMap((window) => window.model_ids ?? []))),
)

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

function authTone(): 'success' | 'warning' | 'danger' | 'neutral' {
  if (props.item?.auth_state === 'ready') return 'success'
  if (props.item?.auth_state === 'refreshing') return 'warning'
  return 'danger'
}
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="item?.mask ?? t('group.credentials.subscription.drawerTitle')"
    :description="t('group.credentials.subscription.drawerDescription')"
    show-description
    :dismissible="!busy"
    :close-label="t('common.close')"
    @update:open="emit('update:open', $event)"
  >
    <div v-if="item" class="subscription-account-drawer">
      <div class="subscription-account-drawer__meta">
        <StatusBadge :tone="authTone()" size="compact">
          {{ t(`group.credentials.subscription.auth.${item.auth_state}`) }}
        </StatusBadge>
        <StatusBadge :status="item.effective_status" size="compact">
          {{ t(`group.credentials.effective.${item.effective_status}`) }}
        </StatusBadge>
      </div>

      <dl class="subscription-account-drawer__identity">
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
        <div v-if="snapshot?.reset_credits_available !== undefined">
          <dt>{{ t('group.credentials.subscription.resetCredits') }}</dt>
          <dd>{{ n(snapshot.reset_credits_available) }}</dd>
        </div>
      </dl>

      <InlineFeedback
        v-if="observation?.state !== 'fresh'"
        :tone="observation?.state === 'error' ? 'warning' : 'neutral'"
        appearance="ledger"
      >
        {{ t(`group.credentials.subscription.observation.${observation?.state ?? 'unavailable'}`) }}
      </InlineFeedback>

      <section class="subscription-account-drawer__section">
        <h3>{{ t('group.credentials.subscription.quotas') }}</h3>
        <p v-if="quotaWindows.length === 0" class="subscription-account-drawer__faint">
          {{ t('group.credentials.subscription.noQuota') }}
        </p>
        <div v-else class="subscription-account-drawer__quotas">
          <article v-for="window in quotaWindows" :key="window.id">
            <div class="subscription-account-drawer__quota-heading">
              <strong>{{ window.label }}</strong>
              <span>{{ quotaValue(window) }}</span>
            </div>
            <div
              class="subscription-account-drawer__progress"
              role="progressbar"
              :aria-label="window.label"
              :aria-valuenow="percent(window)"
              aria-valuemin="0"
              aria-valuemax="100"
            >
              <span
                :class="{ 'is-exhausted': window.state === 'exhausted' }"
                :style="{ width: `${percent(window) ?? 0}%` }"
              />
            </div>
            <div class="subscription-account-drawer__quota-meta">
              <span>{{ t(`group.credentials.subscription.quotaState.${window.state}`) }}</span>
              <span v-if="window.reset_at_ms">
                {{ t('group.credentials.subscription.resetAt') }}
                <AppDateTime :instant="window.reset_at_ms" :locale="locale" />
              </span>
            </div>
          </article>
        </div>
      </section>

      <section class="subscription-account-drawer__section">
        <h3>{{ t('group.credentials.subscription.modelConstraints') }}</h3>
        <p v-if="constrainedModels.length === 0" class="subscription-account-drawer__faint">
          {{ t('group.credentials.subscription.noModelConstraints') }}
        </p>
        <div v-else class="subscription-account-drawer__models">
          <code v-for="model in constrainedModels" :key="model">{{ model }}</code>
        </div>
      </section>

      <section class="subscription-account-drawer__section">
        <h3>{{ t('group.credentials.subscription.syncInfo') }}</h3>
        <dl class="subscription-account-drawer__sync">
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
        </dl>
      </section>
    </div>

    <template v-if="item" #footer>
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
    </template>
  </AppDrawer>
</template>

<style scoped>
.subscription-account-drawer {
  display: grid;
  gap: var(--space-4);
  padding-block: var(--space-3-5);
}
.subscription-account-drawer__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.subscription-account-drawer__identity,
.subscription-account-drawer__sync {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
  margin: 0;
}
.subscription-account-drawer__identity > div,
.subscription-account-drawer__sync > div {
  min-width: 0;
  border-left: 2px solid var(--color-border-control);
  padding-left: var(--space-2);
}
.subscription-account-drawer dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-account-drawer dd {
  margin: 4px 0 0;
  overflow-wrap: anywhere;
  font-size: var(--text-meta);
}
.subscription-account-drawer code {
  font-family: var(--font-mono);
}
.subscription-account-drawer__section {
  display: grid;
  gap: var(--space-3);
}
.subscription-account-drawer__section h3,
.subscription-account-drawer__faint {
  margin: 0;
}
.subscription-account-drawer__section h3 {
  font-size: var(--text-meta);
  font-weight: 650;
}
.subscription-account-drawer__faint {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.subscription-account-drawer__quotas {
  display: grid;
  gap: var(--space-2);
}
.subscription-account-drawer__models {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.subscription-account-drawer__models code {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 5px 8px;
  font-size: var(--text-label-xs);
}
.subscription-account-drawer__quotas article {
  display: grid;
  gap: 8px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 11px 12px;
}
.subscription-account-drawer__quota-heading,
.subscription-account-drawer__quota-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.subscription-account-drawer__quota-heading strong {
  font-size: var(--text-sm);
}
.subscription-account-drawer__quota-heading span,
.subscription-account-drawer__quota-meta {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}
.subscription-account-drawer__progress {
  height: 6px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--color-border-subtle);
}
.subscription-account-drawer__progress > span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--color-action);
}
.subscription-account-drawer__progress > span.is-exhausted {
  background: var(--color-danger);
}
@media (max-width: 520px) {
  .subscription-account-drawer__identity,
  .subscription-account-drawer__sync {
    grid-template-columns: 1fr;
  }
}
</style>
