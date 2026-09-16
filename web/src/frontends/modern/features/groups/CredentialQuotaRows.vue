<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialQuota } from '@modern/api/credential-observation'
import { AppOverflowText, AppProgressBar } from '@modern/components/ui'
import { useClock } from '@modern/components/ui/clock'
import { formatRemainingDuration } from '@modern/components/ui/format'
import {
  quotaCycleTime,
  quotaWindowTitle,
  quotaWindowRange,
  quotaRemaining,
  quotaTone,
  sortedQuotaWindows,
} from './credential-presentation'
const props = defineProps<{ windows: readonly CredentialQuota[] }>()
const { t, te, n, locale } = useI18n()
const now = useClock()
const rows = computed(() => sortedQuotaWindows(props.windows))
function label(window: CredentialQuota): string {
  const key = 'credentialCards.quotaLabels.' + window.labelKey
  return quotaWindowTitle(window, window.labelKey && te(key) ? t(key) : window.label)
}
function value(window: CredentialQuota): string {
  const percent = quotaRemaining(window)
  if (percent !== undefined) return n(Math.round(percent)) + '%'
  if (window.remaining !== undefined) return n(window.remaining)
  return '—'
}
function period(window: CredentialQuota): string {
  const range = quotaWindowRange(window)
  return range
    ? `${quotaCycleTime(range.start)} – ${quotaCycleTime(range.end)}`
    : quotaCycleTime(window.resetsAt)
}
function countdown(window: CredentialQuota): string {
  const remaining = window.resetsAt! - now.value
  return remaining > 0
    ? formatRemainingDuration(remaining, locale.value)
    : t('credentialCards.windowEnded')
}
</script>
<template>
  <div class="modern-credential-quota-list">
    <div v-for="window in rows" :key="window.id" class="modern-credential-quota">
      <div
        class="modern-credential-quota-label"
        :class="{ 'is-tight': quotaTone(window) === 'danger' }"
      >
        <AppOverflowText v-if="label(window)" :text="label(window)" /><span>{{
          value(window)
        }}</span>
      </div>
      <AppProgressBar
        :label="[label(window), value(window)].filter(Boolean).join(' · ')"
        :value="quotaRemaining(window)"
        :tone="quotaTone(window)"
        size="sm"
      />
      <div v-if="window.resetsAt || window.models.length" class="modern-credential-quota-note">
        <AppOverflowText
          v-if="window.models.length"
          class="modern-credential-quota-models"
          :text="window.models.join(' · ')"
        />
        <AppOverflowText v-if="window.resetsAt" :text="period(window)" />
        <time
          v-if="window.resetsAt"
          class="modern-credential-quota-countdown"
          :datetime="new Date(window.resetsAt).toISOString()"
          >{{ countdown(window) }}</time
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
.modern-credential-quota-label {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
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
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  row-gap: var(--modern-space-0-5);
  font-size: var(--modern-font-size-caption);
  color: var(--modern-muted);
}
.modern-credential-quota-note > :first-child {
  min-width: 0;
}
.modern-credential-quota-models {
  grid-column: 1 / -1;
}
.modern-credential-quota-countdown {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
</style>
