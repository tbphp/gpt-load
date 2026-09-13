<script setup lang="ts">
import { Check, X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import { AppIcon, AppOverflowText, AppTooltip } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
defineProps<{ usage: CredentialRow['daily']; compact?: boolean }>()
const { t, n, locale } = useI18n()
</script>
<template>
  <div class="modern-credential-outcomes" :class="{ 'is-compact': compact }">
    <AppTooltip :label="usage && !usage.complete ? t('groups.row.partialHelp') : undefined">
      <span
        class="modern-credential-outcomes-window"
        :tabindex="usage && !usage.complete ? 0 : undefined"
        >24h{{ usage && !usage.complete ? '*' : '' }}</span
      >
    </AppTooltip>
    <template v-if="usage && compact">
      <AppTooltip :label="`${t('credentialCards.successShort')} ${n(usage.successes)}`">
        <span class="modern-credential-outcomes-count" tabindex="0"
          ><AppIcon :icon="Check" size="xs" />{{
            formatCompactNumber(usage.successes, locale)
          }}</span
        >
      </AppTooltip>
      <AppTooltip :label="`${t('credentialCards.failureShort')} ${n(usage.failures)}`">
        <span
          class="modern-credential-outcomes-count"
          :class="{ 'has-failures': usage.failures > 0 }"
          tabindex="0"
          ><AppIcon :icon="X" size="xs" />{{ formatCompactNumber(usage.failures, locale) }}</span
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
.modern-credential-outcomes-partial {
  font-size: var(--modern-font-size-caption);
}
</style>
