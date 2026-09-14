<script setup lang="ts">
import { TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LogEntry } from '@modern/api/logs'
import { AppIcon, AppTooltip } from '@modern/components/ui'
import { logModelMismatch } from './log-display'

const props = defineProps<{ row: LogEntry; detail?: boolean }>()
const { t } = useI18n()
const mismatch = computed(() => logModelMismatch(props.row))
const description = computed(() =>
  t('logs.returnedModelMismatchHint', {
    requested: props.row.upstream_model,
    returned: props.row.upstream_reported_model,
  }),
)
</script>

<template>
  <div v-if="detail && mismatch" class="modern-log-model-warning is-detail" role="note">
    <AppIcon :icon="TriangleAlert" size="sm" />
    <div>
      <strong>{{ t('logs.returnedModelMismatch') }}</strong>
      <p>{{ description }}</p>
    </div>
  </div>
  <AppTooltip v-else-if="mismatch" :label="t('logs.returnedModelMismatch') + '\n' + description">
    <span
      class="modern-log-model-warning"
      tabindex="0"
      :aria-label="t('logs.returnedModelMismatch') + '\n' + description"
    >
      <AppIcon :icon="TriangleAlert" size="inherit" />
    </span>
  </AppTooltip>
</template>

<style scoped>
.modern-log-model-warning {
  display: inline-flex;
  flex: none;
  width: fit-content;
  align-items: center;
  color: var(--modern-danger);
}
.modern-log-model-warning:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-danger);
  outline-offset: var(--modern-focus-offset);
}
.modern-log-model-warning.is-detail {
  width: 100%;
  align-items: flex-start;
  gap: var(--modern-space-2);
  padding: var(--modern-space-2) var(--modern-space-3);
  border-radius: var(--modern-radius-small);
  background: var(--modern-danger-soft);
  font-size: var(--modern-font-size-small);
}
.modern-log-model-warning p {
  margin: var(--modern-space-1) 0 0;
  white-space: pre-line;
  overflow-wrap: anywhere;
}
</style>
