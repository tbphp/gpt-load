<script setup lang="ts">
import { ChevronDown, ChevronUp, RefreshCw, RotateCcw } from '@lucide/vue'
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import type { GroupRow } from '@modern/api/groups'
import type { GroupChannel } from '@modern/api/group-create'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppCheckbox,
  AppIconButton,
  AppLoadingIndicator,
  AppNotice,
  AppOverflowText,
  AppSwitch,
  AppTooltip,
} from '@modern/components/ui'
import CredentialAccountInfo from './CredentialAccountInfo.vue'
import CredentialCardActions from './CredentialCardActions.vue'
import CredentialOutcomeSummary from './CredentialOutcomeSummary.vue'
import CredentialQuotaRows from './CredentialQuotaRows.vue'
import { credentialStatus, credentialTime } from './credential-presentation'

const props = defineProps<{
  row: CredentialRow
  group: GroupRow
  channel?: GroupChannel
  selected: boolean
  disabled: boolean
  pending?: boolean
  pendingAction?: string
  error?: string
}>()
defineEmits<{ select: [value: boolean]; toggle: [value: boolean]; action: [value: string] }>()
const { t, n, locale } = useI18n()
const detailsId = useId()
const expanded = ref(false)
const state = computed(() => credentialStatus(props.row))
const observation = computed(() => props.row.observation)
const plan = computed(
  () =>
    [...new Set([observation.value?.plan, observation.value?.account?.seat].filter(Boolean))].join(
      ' · ',
    ) || props.group.channelName,
)
const creditLabel = computed(() => {
  const expirations = observation.value?.creditExpirations ?? []
  const available = observation.value?.resetCredits ?? 0
  const details = expirations.slice(0, available).map((time, index) =>
    t('credentialCards.creditExpiry', {
      index: n(index + 1),
      time: time ? credentialTime(time, locale.value) : t('credentialCards.noExpiry'),
    }),
  )
  return [t('credentialCards.useReset'), ...details].join('\n')
})
const syncLabel = computed(() =>
  [
    t('credentialCards.syncQuota'),
    observation.value?.observedAt
      ? `${t('credentialCards.quotaUpdated')} ${credentialTime(observation.value.observedAt, locale.value)}`
      : '',
  ]
    .filter(Boolean)
    .join('\n'),
)
</script>

<template>
  <article
    class="modern-subscription-card"
    :class="{ 'is-selected': selected }"
    :aria-busy="pending || undefined"
  >
    <AppLoadingIndicator :loading="pending" />
    <header class="modern-subscription-card-heading">
      <AppChannelIcon
        :icon="group.channelIcon"
        :name="group.channelName"
        :mark="group.channelMark"
        size="md"
        surface
      />
      <div class="modern-subscription-card-identity">
        <AppOverflowText class="modern-subscription-card-name" :text="row.account || row.mask" />
        <div class="modern-subscription-card-subtitle">
          <AppOverflowText :text="plan" /><AppBadge
            :tone="state.tone"
            variant="plain"
            size="xs"
            dot
            >{{ t(state.key) }}</AppBadge
          >
        </div>
      </div>
      <AppTooltip :label="t('groupDetail.selectCredential', { name: row.account || row.mask })">
        <AppCheckbox
          :model-value="selected"
          :label="t('groupDetail.selectCredential', { name: row.account || row.mask })"
          label-hidden
          :disabled="disabled"
          @update:model-value="$emit('select', $event)"
        />
      </AppTooltip>
    </header>
    <div class="modern-subscription-card-body">
      <div
        v-if="channel?.quotaObservation || observation?.windows.length"
        class="modern-subscription-card-quota"
      >
        <CredentialQuotaRows v-if="observation?.windows.length" :windows="observation.windows" />
        <div v-else class="modern-subscription-card-empty">
          <span>{{ t('credentialCards.noQuota') }}</span
          ><span v-if="observation && observation.state !== 'fresh'">{{
            t('credentialCards.observation.' + observation.state)
          }}</span>
        </div>
      </div>
      <div
        v-if="
          (observation?.windows.length && observation.state !== 'fresh') ||
          row.modelCooldowns.length ||
          row.cooldownUntil ||
          row.failuresInRow
        "
        class="modern-subscription-card-notices"
      >
        <span v-if="observation?.windows.length && observation.state !== 'fresh'">{{
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
      <div class="modern-subscription-card-disclosure">
        <AppButton
          variant="text"
          size="xs"
          :icon="expanded ? ChevronUp : ChevronDown"
          :aria-expanded="expanded"
          :aria-controls="detailsId"
          @click="expanded = !expanded"
          >{{ t('credentialCards.accountInfo') }}</AppButton
        >
        <AppTooltip v-if="channel?.resetCredit && observation?.resetCredits" :label="creditLabel"
          ><AppButton
            :icon="RotateCcw"
            variant="ghost"
            size="xs"
            :disabled="disabled || !row.enabled || row.authState !== 'ready'"
            @click="$emit('action', 'reset')"
            >{{
              t('credentialCards.resetCreditsShort', { count: n(observation.resetCredits) })
            }}</AppButton
          ></AppTooltip
        >
      </div>
      <div v-if="expanded" :id="detailsId" class="modern-subscription-card-details">
        <CredentialAccountInfo :row="row" routing /><AppButton
          variant="text"
          size="xs"
          :disabled="disabled"
          @click="$emit('action', 'details')"
          >{{ t('credentialCards.diagnosticsAndSettings') }}</AppButton
        >
      </div>
      <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
    </div>
    <footer class="modern-subscription-card-footer">
      <CredentialOutcomeSummary :usage="row.daily" />
      <div class="modern-subscription-card-actions">
        <AppIconButton
          v-if="channel?.quotaObservation"
          :icon="RefreshCw"
          :label="syncLabel"
          size="xs"
          :loading="pendingAction === 'quota'"
          :disabled="disabled || row.authState !== 'ready'"
          @click="$emit('action', 'quota')"
        />
        <CredentialCardActions
          :row="row"
          subscription
          :disabled="disabled"
          @action="$emit('action', $event)"
        />
        <AppSwitch
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
.modern-subscription-card {
  container: modern-subscription-card / inline-size;
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
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
  align-items: center;
  gap: var(--modern-space-3);
  padding: var(--modern-credential-card-inset) var(--modern-credential-card-inset)
    var(--modern-space-3);
}
.modern-subscription-card-identity {
  display: grid;
  gap: var(--modern-space-1);
  flex: 1;
  min-width: 0;
}
.modern-subscription-card-name {
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
}
.modern-subscription-card-subtitle {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-card-subtitle > :first-child {
  flex: 0 1 auto;
}
.modern-subscription-card-subtitle > :last-child {
  flex: none;
  margin-left: auto;
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
  min-height: calc(var(--modern-space-12) * 2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-card-empty > :last-child:not(:first-child) {
  color: var(--modern-warning);
}
.modern-subscription-card-notices {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-warning);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-card-disclosure {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-1) var(--modern-space-2);
  min-height: var(--modern-control-xs);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-card-details {
  display: grid;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3) 0;
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-subscription-card-footer {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  padding: var(--modern-space-2) var(--modern-credential-card-inset);
  border-top: var(--modern-line-width) solid var(--modern-border);
  background: color-mix(in srgb, var(--modern-subtle) 35%, var(--modern-surface));
}
.modern-subscription-card-actions {
  display: flex;
  align-items: center;
  flex: none;
  gap: var(--modern-space-1);
  margin-left: auto;
}
@container modern-subscription-card (max-width: 300px) {
  .modern-subscription-card-subtitle {
    flex-wrap: wrap;
    row-gap: var(--modern-space-0-5);
  }
  .modern-subscription-card-subtitle > :last-child {
    margin-left: 0;
  }
}
</style>
