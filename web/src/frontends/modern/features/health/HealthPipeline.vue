<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowUpRight, Database } from '@lucide/vue'
import type { HealthReport } from '@modern/api/health'
import { AppBadge, AppButton, AppIcon } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { pipelineHasHistory } from './health-display'

const props = defineProps<{ report: HealthReport }>()
defineEmits<{ open: [] }>()
const { t, locale } = useI18n()
const count = (value: number) => formatCompactNumber(value, locale.value)
const status = computed(() => {
  const p = props.report.pipeline
  if (p.checkpointDegraded) return { text: 'degraded', tone: 'danger' as const }
  if (p.queue_capacity > 0 && p.queue_depth >= p.queue_capacity)
    return { text: 'fullQueue', tone: 'warning' as const }
  return { text: pipelineHasHistory(props.report) ? 'history' : 'normal', tone: 'neutral' as const }
})
</script>

<template>
  <section class="modern-health-pipeline" :aria-label="t('health.pipeline')">
    <div class="modern-health-pipeline-title">
      <AppIcon :icon="Database" size="sm" /><strong>{{ t('health.pipeline') }}</strong
      ><AppBadge :tone="status.tone" size="xs" dot>{{ t('health.' + status.text) }}</AppBadge>
    </div>
    <dl>
      <div>
        <dt>{{ t('health.queue') }}</dt>
        <dd>
          {{ count(report.pipeline.queue_depth) }}
          <span>/ {{ count(report.pipeline.queue_capacity) }}</span>
        </dd>
      </div>
      <div>
        <dt>{{ t('health.persisted') }}</dt>
        <dd>{{ count(report.pipeline.persisted_total) }}</dd>
      </div>
      <div>
        <dt>{{ t('health.dropped') }}</dt>
        <dd :class="{ 'has-errors': report.pipeline.dropped_total > 0 }">
          {{ count(report.pipeline.dropped_total) }}
        </dd>
      </div>
    </dl>
    <AppButton variant="text" @click="$emit('open')"
      >{{ t('health.pipelineDetail') }}<AppIcon :icon="ArrowUpRight" size="sm"
    /></AppButton>
  </section>
</template>

<style scoped>
.modern-health-pipeline {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3) var(--modern-space-6);
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-block: var(--modern-space-3);
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-health-pipeline-title {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-health-pipeline-title strong {
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
}
.modern-health-pipeline dl {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  gap: var(--modern-space-3) var(--modern-space-6);
}
.modern-health-pipeline dl > div {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-2);
}
.modern-health-pipeline dd {
  margin: 0;
  font-variant-numeric: tabular-nums;
  color: var(--modern-text);
}
.modern-health-pipeline dd span {
  color: var(--modern-muted);
}
.modern-health-pipeline .has-errors {
  color: var(--modern-warning);
}
</style>
