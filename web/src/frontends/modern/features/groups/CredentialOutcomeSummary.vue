<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import { AppOverflowText, AppTooltip } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
defineProps<{ usage: CredentialRow['daily'] }>()
const { t, n, locale } = useI18n()
</script>
<template>
  <div class="modern-credential-outcomes">
    <span class="modern-credential-outcomes-window">24h</span>
    <template v-if="usage">
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
