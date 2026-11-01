<script setup lang="ts">
import { Check, X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import { AppIcon, AppOverflowText, AppTooltip } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
const props = defineProps<{ usage: CredentialRow['daily']; compact?: boolean; numeric?: boolean }>()
const { t, n, locale } = useI18n()

function outcomeLabel(outcome: 'successShort' | 'failureShort', count?: number): string {
  const label = `${props.numeric ? '24h ' : ''}${t(`credentialCards.${outcome}`)} ${count === undefined ? '—' : n(count)}`
  return props.numeric && props.usage && !props.usage.complete
    ? `${label} · ${t('groups.row.partialHelp')}`
    : label
}
</script>
<template>
  <div class="modern-credential-outcomes" :class="{ 'is-compact': compact, 'is-numeric': numeric }">
    <AppTooltip
      v-if="!numeric"
      :label="usage && !usage.complete ? t('groups.row.partialHelp') : undefined"
    >
      <span
        class="modern-credential-outcomes-window"
        :tabindex="usage && !usage.complete ? 0 : undefined"
        >24h{{ usage && !usage.complete ? '*' : '' }}</span
      >
    </AppTooltip>
    <template v-if="compact && (usage || numeric)">
      <AppTooltip :label="outcomeLabel('successShort', usage?.successes)">
        <span class="modern-credential-outcomes-count is-success" tabindex="0"
          ><AppIcon v-if="!numeric" :icon="Check" size="xs" />{{
            usage ? formatCompactNumber(usage.successes, locale) : '—'
          }}</span
        >
      </AppTooltip>
      <span v-if="numeric" aria-hidden="true">·</span>
      <AppTooltip :label="outcomeLabel('failureShort', usage?.failures)">
        <span
          class="modern-credential-outcomes-count is-failure"
          :class="{ 'has-failures': usage && usage.failures > 0 }"
          tabindex="0"
          ><AppIcon v-if="!numeric" :icon="X" size="xs" />{{
            usage ? formatCompactNumber(usage.failures, locale) : '—'
          }}</span
        >
      </AppTooltip>
    </template>
    <template v-else-if="usage">
      <span class="modern-credential-outcomes-count"
        ><AppOverflowText
          :text="formatCompactNumber(usage.successes, locale)"
          :full-text="n(usage.successes)"
        />{{ t('credentialCards.successShort') }}</span
      >
      <span class="modern-credential-outcomes-count" :class="{ 'has-failures': usage.failures > 0 }"
        ><AppOverflowText
          :text="formatCompactNumber(usage.failures, locale)"
          :full-text="n(usage.failures)"
        />{{ t('credentialCards.failureShort') }}</span
      >
      <AppTooltip v-if="!usage.complete" :label="t('groups.row.partialHelp')"
        ><span class="modern-credential-outcomes-partial" tabindex="0">{{
          t('groups.row.partial')
        }}</span></AppTooltip
      >
    </template>
    <span v-else>—</span>
  </div>
</template>
<style scoped>
.modern-credential-outcomes {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  align-items: baseline;
  column-gap: var(--modern-space-2);
  row-gap: var(--modern-space-0-5);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-credential-outcomes-window {
  font-size: var(--modern-font-size-caption);
}
.modern-credential-outcomes.is-compact {
  flex-wrap: nowrap;
  align-items: center;
  gap: var(--modern-space-1);
  font-size: var(--modern-font-size-caption);
  line-height: var(--modern-leading-compact);
}
.modern-credential-outcomes.is-compact .modern-credential-outcomes-count {
  align-items: center;
  gap: var(--modern-space-0-5);
}
.modern-credential-outcomes-count {
  display: inline-flex;
  align-items: baseline;
  gap: var(--modern-space-1);
}
.modern-credential-outcomes-count.has-failures {
  color: var(--modern-danger);
}
.modern-credential-outcomes.is-numeric .is-success {
  color: var(--modern-success);
}
.modern-credential-outcomes.is-numeric .is-failure {
  color: var(--modern-danger);
}
.modern-credential-outcomes-partial {
  font-size: var(--modern-font-size-caption);
}
</style>
