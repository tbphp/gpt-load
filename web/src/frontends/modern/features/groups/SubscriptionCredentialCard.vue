<script setup lang="ts">
import { useLoadingActivity } from '@modern/components/ui/loading'
import { Check, RefreshCw, Ticket } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import type { GroupChannel } from '@modern/api/group-create'
import {
  AppBadge,
  AppButton,
  AppCheckbox,
  AppIconButton,
  AppOverflowText,
  AppSwitch,
  AppTooltip,
} from '@modern/components/ui'
import CredentialRoutingMeta from './CredentialRoutingMeta.vue'
import './credential-card.css'
import CredentialCardActions from './CredentialCardActions.vue'
import CredentialOutcomeSummary from './CredentialOutcomeSummary.vue'
import CredentialQuotaRows from './CredentialQuotaRows.vue'
import CredentialPlanBadge from './CredentialPlanBadge.vue'
import { credentialStatus, credentialTime } from './credential-presentation'

const props = defineProps<{
  row: CredentialRow
  channel?: GroupChannel
  selected: boolean
  disabled: boolean
  pending?: boolean
  pendingAction?: string
  syncSucceeded?: boolean
  error?: string
}>()
defineEmits<{ select: [value: boolean]; toggle: [value: boolean]; action: [value: string] }>()
const { t, n, locale } = useI18n()
const state = computed(() => credentialStatus(props.row))
const observation = computed(() => props.row.observation)
const plan = computed(() => observation.value?.plan.trim() ?? '')
const creditLabel = computed(() => {
  const expirations = observation.value?.creditExpirations ?? []
  const available = observation.value?.resetCredits ?? 0
  const details = expirations.slice(0, available).map((time, index) =>
    t('credentialCards.creditExpiry', {
      index: n(index + 1),
      time: time ? credentialTime(time, locale.value) : t('credentialCards.noExpiry'),
    }),
  )
  return [t('credentialCards.resetCredits', { count: n(available) }), ...details].join('\n')
})
const syncLabel = computed(() =>
  props.syncSucceeded
    ? t('credentialCards.syncSucceeded')
    : [
        t('credentialCards.syncQuota'),
        observation.value?.observedAt
          ? `${t('credentialCards.quotaUpdated')} ${credentialTime(observation.value.observedAt, locale.value)}`
          : '',
      ]
        .filter(Boolean)
        .join('\n'),
)
const syncIcon = computed(() => (props.syncSucceeded ? Check : RefreshCw))
useLoadingActivity(() => Boolean(props.pending))
</script>

<template>
  <article
    class="modern-subscription-card modern-credential-surface modern-credential-surface--subscription"
    :class="{ 'is-selected': selected }"
    :aria-busy="pending || undefined"
  >
    <header class="modern-subscription-card-heading">
      <AppTooltip :label="t('groupDetail.selectCredential', { name: row.account || row.mask })">
        <AppCheckbox
          class="modern-subscription-card-select"
          :model-value="selected"
          :label="t('groupDetail.selectCredential', { name: row.account || row.mask })"
          label-hidden
          :disabled="disabled"
          @update:model-value="$emit('select', $event)"
        />
      </AppTooltip>
      <div class="modern-subscription-card-identity">
        <div class="modern-subscription-card-name-line">
          <AppOverflowText class="modern-subscription-card-name" :text="row.account || row.mask" />
          <AppTooltip v-if="row.rpmPeakHour !== undefined" :label="t('rpm.hourPeak')">
            <span class="modern-subscription-card-rpm" tabindex="0"
              >{{ t('rpm.cardLabel') }} {{ n(row.rpmPeakHour) }}</span
            >
          </AppTooltip>
        </div>
        <div class="modern-subscription-card-subtitle">
          <div class="modern-subscription-card-plan">
            <CredentialPlanBadge v-if="plan" :name="plan" :level="observation?.planLevel" />
            <CredentialRoutingMeta :row="row" />
          </div>
          <div class="modern-subscription-card-status-actions">
            <AppIconButton
              v-if="channel?.quotaObservation"
              class="modern-subscription-card-sync"
              :class="{ 'is-succeeded': syncSucceeded }"
              :icon="syncIcon"
              :label="syncLabel"
              size="xs"
              :loading="pendingAction === 'quota'"
              :disabled="disabled || row.authState !== 'ready'"
              @click="$emit('action', 'quota')"
            />
            <AppBadge
              class="modern-subscription-card-status"
              :tone="state.tone"
              variant="plain"
              size="xs"
              dot
            >
              <AppOverflowText :text="t(state.key)" />
            </AppBadge>
          </div>
        </div>
      </div>
    </header>
    <div class="modern-subscription-card-body">
      <div
        v-if="channel?.quotaObservation || observation?.windows.length"
        class="modern-subscription-card-quota"
      >
        <CredentialQuotaRows v-if="observation?.windows.length" :windows="observation.windows" />
        <div v-else class="modern-subscription-card-empty">
          <span>{{ t('credentialCards.noQuota') }}</span>
          <div class="modern-subscription-card-empty-row">
            <span
              v-if="observation && observation.state !== 'fresh'"
              class="modern-subscription-card-empty-state"
              >{{ t('credentialCards.observation.' + observation.state) }}</span
            >
            <AppButton
              v-if="channel?.quotaObservation"
              class="modern-subscription-card-sync"
              :class="{ 'is-succeeded': syncSucceeded }"
              size="xxs"
              :icon="syncIcon"
              :loading="pendingAction === 'quota'"
              :disabled="disabled || row.authState !== 'ready'"
              @click="$emit('action', 'quota')"
              >{{ t('credentialCards.syncQuota') }}</AppButton
            >
          </div>
        </div>
      </div>
      <div
        v-if="
          error ||
          (observation?.windows.length && observation.state !== 'fresh') ||
          row.modelCooldowns.length ||
          row.cooldownUntil ||
          row.failuresInRow
        "
        class="modern-subscription-card-notices"
      >
        <span v-if="error" class="modern-subscription-card-error" role="alert">{{ error }}</span>
        <span v-else-if="observation?.windows.length && observation.state !== 'fresh'">{{
          t('credentialCards.observation.' + observation.state)
        }}</span>
        <span v-if="row.cooldownUntil">{{
          t('groupDetail.recoversAt', { time: credentialTime(row.cooldownUntil, locale) })
        }}</span>
        <span v-if="row.failuresInRow">{{
          t('credentialCards.consecutiveFailures', { count: n(row.failuresInRow) })
        }}</span>
        <AppButton
          v-if="row.modelCooldowns.length"
          variant="text"
          size="xs"
          :disabled="disabled"
          @click="$emit('action', 'details')"
          >{{
            t('credentialCards.modelCooldowns', { count: n(row.modelCooldowns.length) })
          }}</AppButton
        >
      </div>
    </div>
    <footer class="modern-subscription-card-footer modern-credential-surface-footer">
      <CredentialOutcomeSummary :usage="row.daily" compact />
      <div class="modern-subscription-card-actions">
        <AppTooltip v-if="channel?.resetCredit && observation?.resetCredits" :label="creditLabel">
          <AppButton
            class="modern-subscription-card-reset"
            :icon="Ticket"
            variant="outline"
            size="xxs"
            :aria-label="`${t('credentialCards.useReset')} · ${t('credentialCards.resetCreditsShort', { count: n(observation.resetCredits) })}`"
            :loading="pendingAction === 'reset'"
            :disabled="disabled || !row.enabled || row.authState !== 'ready'"
            @click="$emit('action', 'reset')"
          >
            {{ t('credentialCards.resetAction') }}
            <span class="modern-subscription-card-reset-count" aria-hidden="true">{{
              n(observation.resetCredits)
            }}</span>
          </AppButton>
        </AppTooltip>
        <CredentialCardActions
          :row="row"
          subscription
          :disabled="disabled"
          @action="$emit('action', $event)"
        />
        <AppSwitch
          size="xxs"
          :model-value="row.enabled"
          :label="t('groups.edit.enabled')"
          :disabled="disabled"
          @update:model-value="$emit('toggle', $event)"
        />
      </div>
    </footer>
  </article>
</template>

<style scoped>
.modern-subscription-card-error {
  color: var(--modern-danger);
}
.modern-subscription-card-sync.is-succeeded {
  color: var(--modern-success);
}
.modern-subscription-card {
  container: modern-subscription-card / inline-size;
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  overflow: hidden;
  transition: border-color var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-subscription-card:hover {
  border-color: var(--modern-control-border-hover);
}
.modern-subscription-card.is-selected {
  border-color: var(--modern-accent);
}
.modern-subscription-card-heading {
  display: flex;
  align-items: flex-start;
  gap: var(--modern-space-2);
  padding: var(--modern-credential-card-inset) var(--modern-credential-card-inset)
    var(--modern-space-3);
}
.modern-subscription-card-identity {
  display: grid;
  gap: var(--modern-space-1);
  flex: 1;
  min-width: 0;
}
.modern-subscription-card-name-line {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-subscription-card-select {
  align-self: flex-start;
  margin-top: var(--modern-space-0-5);
}
.modern-subscription-card-name {
  flex: 1;
  min-width: 0;
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
}
.modern-subscription-card-rpm {
  flex: none;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-medium);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.modern-subscription-card-subtitle {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-card-plan {
  display: flex;
  flex: 1;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-1) var(--modern-space-2);
  min-height: var(--modern-control-xs);
  min-width: 0;
}
.modern-subscription-card-status-actions {
  display: flex;
  flex: 0 1 auto;
  align-self: flex-start;
  align-items: center;
  gap: var(--modern-space-0-5);
  min-height: var(--modern-control-xs);
  min-width: 0;
  max-width: 65%;
  margin-left: auto;
}
.modern-subscription-card-status {
  flex: 0 1 auto;
  min-width: 0;
}
.modern-subscription-card-body {
  display: grid;
  gap: var(--modern-space-3);
  padding: var(--modern-space-1) var(--modern-credential-card-inset) var(--modern-space-2);
  min-width: 0;
}
.modern-subscription-card-quota {
  padding-block: var(--modern-space-1);
}
.modern-subscription-card-empty {
  display: grid;
  align-content: center;
  gap: var(--modern-space-1);
  min-height: var(--modern-space-12);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-card-empty-state {
  color: var(--modern-warning);
}
.modern-subscription-card-empty-row {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  margin-top: var(--modern-space-1);
}
.modern-subscription-card-empty-row > :last-child {
  margin-left: auto;
}
.modern-subscription-card-notices {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-warning);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-card-footer {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  padding: var(--modern-space-1) var(--modern-credential-card-inset);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-subscription-card-footer > :first-child {
  flex: 1 0 auto;
}
.modern-subscription-card-actions {
  display: flex;
  align-items: center;
  flex: none;
  gap: var(--modern-space-1);
  margin-left: auto;
}
/* 重置卡是刻意保留的醒目入口，描边不要去掉。 */
.modern-subscription-card-actions .modern-subscription-card-reset {
  border-color: var(--modern-tooltip-border);
}
.modern-subscription-card-actions .modern-subscription-card-reset:hover:not(:disabled) {
  border-color: var(--modern-accent);
}
.modern-subscription-card-reset-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: var(--modern-space-4);
  padding-inline: var(--modern-space-1);
  border-radius: var(--modern-radius-small);
  background: var(--modern-accent-soft);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
</style>
