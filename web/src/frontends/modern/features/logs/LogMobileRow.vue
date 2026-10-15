<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { LogEntry } from '@modern/api/logs'
import { AppBadge, AppButton, AppIcon } from '@modern/components/ui'
import { logDuration, logMoney, logStatusTone, logTime } from './log-display'

defineProps<{ row: LogEntry; groupName?: string }>()
defineEmits<{ open: [] }>()
const { t, locale } = useI18n()
</script>

<template>
  <AppButton
    variant="ghost"
    class="modern-log-mobile"
    :aria-label="`${t('logs.details')}: ${row.client_model ?? '—'}, ${logTime(row.completed_at_ms, locale)}`"
    @click="$emit('open')"
  >
    <span class="modern-log-mobile-heading"
      ><strong>{{ row.client_model || row.operation || '—' }}</strong
      ><AppBadge :tone="logStatusTone[row.status]" variant="plain" size="xs" dot>{{
        t('logs.values.' + row.status)
      }}</AppBadge
      ><AppIcon :icon="ChevronRight" size="sm"
    /></span>
    <span class="modern-log-mobile-source">{{ groupName || row.access_key.name || '—' }}</span>
    <span class="modern-log-mobile-meta"
      ><time :datetime="new Date(row.completed_at_ms).toISOString()">{{
        logTime(row.completed_at_ms, locale)
      }}</time
      ><span>{{ logDuration(row.duration_ms, locale) }}</span
      ><span v-if="row.cost_state === 'priced'">{{
        logMoney(row.estimated_cost_nano_usd, locale)
      }}</span></span
    >
    <span v-if="row.error_code || row.error_summary" class="modern-log-mobile-error">{{
      row.error_code || row.error_summary
    }}</span>
  </AppButton>
</template>

<style scoped>
.modern-log-mobile {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  justify-content: stretch;
  color: var(--modern-text);
  width: 100%;
  gap: var(--modern-space-2);
  height: auto;
  padding: var(--modern-space-4) 0;
  border: 0;
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  border-radius: 0;
  text-align: left;
  white-space: normal;
  font-weight: var(--modern-weight-regular);
}
.modern-log-mobile-heading {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-log-mobile-heading strong {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: var(--modern-weight-semibold);
}
.modern-log-mobile-source {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-mobile-meta {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-log-mobile-error {
  color: var(--modern-danger);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--modern-font-size-small);
}
</style>
