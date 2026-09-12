<script setup lang="ts">
import { ChevronDown, ChevronUp } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialQuota } from '@modern/api/credential-observation'
import { AppButton, AppOverflowText, AppProgressBar } from '@modern/components/ui'
import {
  credentialTime,
  quotaPeriod,
  quotaRemaining,
  sortedQuotaWindows,
} from './credential-presentation'
const props = defineProps<{ windows: readonly CredentialQuota[] }>()
const { t, te, n, locale } = useI18n()
const expanded = ref(false)
const rows = computed(() => sortedQuotaWindows(props.windows))
const primary = computed(() => {
  const account = rows.value.filter((window) => window.scope === 'account')
  return (account.length ? account : rows.value).slice(0, 3)
})
const other = computed(() => rows.value.filter((window) => !primary.value.includes(window)))
const otherTight = computed(
  () =>
    other.value.filter(
      (window) => window.state === 'exhausted' || (quotaRemaining(window) ?? 100) < 30,
    ).length,
)
const visible = computed(() => (expanded.value ? rows.value : primary.value))
function label(window: CredentialQuota): string {
  const period = quotaPeriod(window.windowSeconds)
  const key = 'credentialCards.quotaLabels.' + window.labelKey
  const subject = window.labelKey && te(key) ? t(key) : window.label
  if (window.scope === 'account' && period)
    return subject && !subject.toLowerCase().includes(period.toLowerCase())
      ? `${subject} · ${period}`
      : subject || period
  return subject
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
    <div v-for="window in visible" :key="window.id" class="modern-credential-quota">
      <div class="modern-credential-quota-label">
        <AppOverflowText :text="label(window)" /><span
          :class="{ 'is-tight': tone(window) === 'danger' }"
          >{{ value(window) }}</span
        >
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
    <AppButton
      v-if="other.length"
      variant="text"
      size="xs"
      class="modern-credential-quota-more"
      :icon="expanded ? ChevronUp : ChevronDown"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
      >{{
        t(expanded ? 'credentialCards.collapseWindows' : 'credentialCards.moreWindows', {
          count: n(other.length),
        })
      }}<span v-if="!expanded && otherTight">{{
        t('credentialCards.tightWindows', { count: n(otherTight) })
      }}</span></AppButton
    >
  </div>
</template>
<style scoped>
.modern-credential-quota-list {
  display: grid;
  gap: var(--modern-space-4);
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
}
.modern-credential-quota-label > :last-child {
  margin-left: auto;
  flex: none;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
  font-variant-numeric: tabular-nums;
  color: var(--modern-text);
}
.modern-credential-quota-label > .is-tight {
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
.modern-credential-quota-more {
  max-width: 100%;
  flex-wrap: wrap;
  justify-content: flex-start;
  color: var(--modern-muted);
}
.modern-credential-quota-more > span {
  color: var(--modern-warning);
}
</style>
