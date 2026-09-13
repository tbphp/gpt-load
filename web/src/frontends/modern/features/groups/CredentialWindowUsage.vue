<script setup lang="ts">
import { Info } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialQuota } from '@modern/api/credential-observation'
import { AppIcon, AppOverflowText, AppTooltip } from '@modern/components/ui'
import { formatCompactNumber, formatNanoUSD } from '@modern/components/ui/format'
import { credentialTime, quotaWindowTitle, sortedQuotaWindows } from './credential-presentation'

const props = defineProps<{ windows: readonly CredentialQuota[] }>()
const { t, te, n, locale } = useI18n()
const rows = computed(() => sortedQuotaWindows(props.windows))
const peak = computed(() => Math.max(1, ...rows.value.map((window) => window.usage?.requests ?? 0)))
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
      <span>{{ t('credentialCards.compareRequests') }}</span>
    </header>
    <div
      class="modern-window-usage-table"
      role="table"
      :aria-label="t('credentialCards.windowUsage')"
    >
      <div class="modern-window-usage-columns modern-window-usage-head" role="row">
        <span role="columnheader">{{ t('credentialCards.window') }}</span>
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
        <span
          class="modern-window-usage-bar"
          :style="{ width: `${((window.usage?.requests ?? 0) / peak) * 100}%` }"
          aria-hidden="true"
        />
        <div class="modern-window-usage-name" role="cell">
          <AppOverflowText :text="label(window)" :full-text="detail(window)" />
          <AppTooltip v-if="warning(window)" :label="warning(window)">
            <span class="modern-window-usage-warning" tabindex="0" :aria-label="warning(window)"
              ><AppIcon :icon="Info" size="xs"
            /></span>
          </AppTooltip>
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
          <AppOverflowText :text="window.usage ? formatNanoUSD(window.usage.cost, locale) : '—'" />
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
.modern-window-usage-heading > span {
  margin-left: auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-window-usage-table {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  overflow: hidden;
}
.modern-window-usage-columns {
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) minmax(0, 0.8fr) minmax(0, 0.85fr) minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-2);
  padding: var(--modern-space-2);
  text-align: left;
}
.modern-window-usage-head {
  background: var(--modern-subtle);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-window-usage-row {
  position: relative;
  min-height: var(--modern-space-10);
  border-top: var(--modern-line-width) solid var(--modern-border);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-window-usage-bar {
  position: absolute;
  inset-block: var(--modern-space-0-5);
  left: 0;
  max-width: 100%;
  pointer-events: none;
  border-radius: var(--modern-radius-small);
  background: linear-gradient(90deg, var(--modern-key-card-tint), var(--modern-accent-soft));
}
.modern-window-usage-row > div {
  position: relative;
  min-width: 0;
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
