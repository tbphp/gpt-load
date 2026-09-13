<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialQuota } from '@modern/api/credential-observation'
import { AppOverflowText, AppProgressBar } from '@modern/components/ui'
import {
  credentialTime,
  quotaWindowTitle,
  quotaRemaining,
  sortedQuotaWindows,
} from './credential-presentation'
const props = defineProps<{ windows: readonly CredentialQuota[] }>()
const { t, te, n, locale } = useI18n()
const rows = computed(() => sortedQuotaWindows(props.windows))
function label(window: CredentialQuota): string {
  const key = 'credentialCards.quotaLabels.' + window.labelKey
  return quotaWindowTitle(window, window.labelKey && te(key) ? t(key) : window.label)
}
function value(window: CredentialQuota): string {
  const percent = quotaRemaining(window)
  if (percent !== undefined)
    return t('credentialCards.remaining', { value: n(Math.round(percent)) + '%' })
  if (window.remaining !== undefined)
    return t('credentialCards.remaining', { value: n(window.remaining) })
  return '—'
}
function tone(window: CredentialQuota): 'neutral' | 'success' | 'warning' | 'danger' {
  const percent = quotaRemaining(window)
  if (window.state === 'exhausted') return 'danger'
  return percent === undefined
    ? 'neutral'
    : percent < 30
      ? 'danger'
      : percent < 70
        ? 'warning'
        : 'success'
}
</script>
<template>
  <div class="modern-credential-quota-list">
    <div v-for="window in rows" :key="window.id" class="modern-credential-quota">
      <div class="modern-credential-quota-label" :class="{ 'is-tight': tone(window) === 'danger' }">
        <AppOverflowText :text="label(window)" /><span>{{ value(window) }}</span>
      </div>
      <AppProgressBar
        :label="`${label(window)} · ${value(window)}`"
        :value="quotaRemaining(window)"
        :tone="tone(window)"
        size="sm"
      />
      <div v-if="window.resetsAt || window.models.length" class="modern-credential-quota-note">
        <AppOverflowText v-if="window.models.length" :text="window.models.join(' · ')" /><span
          v-if="window.resetsAt"
          >{{
            t(
              window.resetsAt <= Date.now()
                ? 'credentialCards.windowExpired'
                : 'credentialCards.resetsAt',
              { time: credentialTime(window.resetsAt, locale) },
            )
          }}</span
        >
      </div>
    </div>
  </div>
</template>
<style scoped>
.modern-credential-quota-list {
  display: grid;
  gap: var(--modern-space-3);
}
.modern-credential-quota {
  display: grid;
  gap: var(--modern-space-1);
  min-width: 0;
}
.modern-credential-quota-label,
.modern-credential-quota-note {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-credential-quota-label {
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
  color: var(--modern-muted);
}
.modern-credential-quota-label > :last-child {
  margin-left: auto;
  flex: none;
  font-variant-numeric: tabular-nums;
}
.modern-credential-quota-label.is-tight {
  color: var(--modern-danger);
}
.modern-credential-quota-note {
  flex-wrap: wrap;
  row-gap: var(--modern-space-0-5);
  font-size: var(--modern-font-size-caption);
  color: var(--modern-muted);
}
.modern-credential-quota-note > :first-child {
  min-width: 0;
}
.modern-credential-quota-note > span {
  flex-shrink: 0;
}
</style>
