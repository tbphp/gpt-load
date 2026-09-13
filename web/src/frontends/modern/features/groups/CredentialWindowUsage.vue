<script setup lang="ts">
import { Info } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialQuota } from '@modern/api/credential-observation'
import { AppIcon, AppOverflowText, AppTooltip } from '@modern/components/ui'
import { formatCompactNumber, formatNanoUSD } from '@modern/components/ui/format'
import {
  credentialTime,
  quotaRemaining,
  quotaWindowTitle,
  sortedQuotaWindows,
} from './credential-presentation'

const props = defineProps<{ windows: readonly CredentialQuota[] }>()
const { t, te, n, locale } = useI18n()
const rows = computed(() => sortedQuotaWindows(props.windows))
function remaining(window: CredentialQuota): string {
  const value = quotaRemaining(window)
  if (value !== undefined) return `${n(value, { maximumFractionDigits: 1 })}%`
  return window.remaining === undefined ? '—' : n(window.remaining)
}
function label(window: CredentialQuota): string {
  const key = 'credentialCards.quotaLabels.' + window.labelKey
  return quotaWindowTitle(window, window.labelKey && te(key) ? t(key) : window.label)
}
function detail(window: CredentialQuota): string {
  const usage = window.usage
  return [
    label(window),
    window.models.join(' · '),
    usage?.from && usage.to
      ? `${credentialTime(usage.from, locale.value)} – ${credentialTime(usage.to, locale.value)}`
      : '',
  ]
    .filter(Boolean)
    .join('\n')
}
function warning(window: CredentialQuota): string | undefined {
  const usage = window.usage
  if (!usage) return t('credentialCards.usageUnavailable')
  const parts = [
    !usage.complete ? t('groups.row.partialHelp') : '',
    !usage.usageComplete ? t('credentialCards.tokensIncomplete') : '',
    !usage.pricingComplete ? t('credentialCards.pricingIncomplete') : '',
  ].filter(Boolean)
  return parts.length ? parts.join('\n') : undefined
}
</script>

<template>
  <section class="modern-window-usage">
    <header class="modern-window-usage-heading">
      <h3>{{ t('credentialCards.windowUsage') }}</h3>
    </header>
    <div
      class="modern-window-usage-table"
      role="table"
      :aria-label="t('credentialCards.windowUsage')"
    >
      <div class="modern-window-usage-columns modern-window-usage-head" role="row">
        <span role="columnheader">{{ t('credentialCards.window') }}</span>
        <span role="columnheader">{{ t('credentialCards.remainingShort') }}</span>
        <span role="columnheader">{{ t('credentialCards.requests') }}</span>
        <span role="columnheader">Tokens</span>
        <span role="columnheader">{{ t('credentialCards.costShort') }}</span>
      </div>
      <div
        v-for="window in rows"
        :key="window.id"
        class="modern-window-usage-columns modern-window-usage-row"
        role="row"
      >
        <div class="modern-window-usage-name" role="cell">
          <AppOverflowText :text="label(window)" :full-text="detail(window)" />
          <AppTooltip v-if="warning(window)" :label="warning(window)">
            <span class="modern-window-usage-warning" tabindex="0" :aria-label="warning(window)"
              ><AppIcon :icon="Info" size="xs"
            /></span>
          </AppTooltip>
        </div>
        <div role="cell" class="modern-window-usage-remaining">
          <span>{{ remaining(window) }}</span>
        </div>
        <div role="cell" class="modern-window-usage-requests">
          <AppOverflowText
            :text="window.usage ? formatCompactNumber(window.usage.requests, locale) : '—'"
            :full-text="window.usage ? n(window.usage.requests) : undefined"
          />
        </div>
        <div role="cell">
          <AppOverflowText
            :text="window.usage ? formatCompactNumber(window.usage.tokens, locale) : '—'"
            :full-text="window.usage ? n(window.usage.tokens) : undefined"
          />
        </div>
        <div role="cell">
          <AppOverflowText
            :text="window.usage ? formatNanoUSD(window.usage.cost, locale, 'narrowSymbol') : '—'"
          />
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.modern-window-usage {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-window-usage-heading {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-window-usage-heading h3 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-window-usage-table {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  overflow: hidden;
}
.modern-window-usage-columns {
  display: grid;
  grid-template-columns:
    minmax(0, 1.35fr) minmax(0, 0.9fr) minmax(0, 0.8fr) minmax(0, 0.85fr)
    minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-2);
  padding: var(--modern-space-2);
  text-align: left;
}
.modern-window-usage-head {
  background: var(--modern-surface);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-window-usage-row {
  min-height: var(--modern-touch-target);
  border-top: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-window-usage-row > div {
  min-width: 0;
}
.modern-window-usage-remaining {
  display: grid;
  gap: var(--modern-space-1);
}
.modern-window-usage-name {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  color: var(--modern-text);
}
.modern-window-usage-warning {
  display: inline-flex;
  flex: none;
  color: var(--modern-warning);
}
.modern-window-usage-requests {
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
}
</style>
