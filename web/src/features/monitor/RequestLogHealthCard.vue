<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { RequestLogHealthDto } from '@/app/resources/health'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import Surface from '@/components/ui/Surface.vue'

import MonitorSectionHeading from './MonitorSectionHeading.vue'

const props = defineProps<{
  stats: RequestLogHealthDto
}>()
const { locale, n, t } = useI18n()

const state = computed(() => {
  if (props.stats.dropped_total > 0 || props.stats.write_failure_total > 0) {
    return { tone: 'danger' as const, label: t('monitor.health.requestLog.abnormal') }
  }
  if (props.stats.retention_delete_failure_total > 0) {
    return { tone: 'warning' as const, label: t('monitor.health.requestLog.retentionAbnormal') }
  }
  return { tone: 'success' as const, label: t('monitor.health.requestLog.normal') }
})

const checkpointState = computed(() => {
  if (props.stats.access_quota_checkpoint_write_failure_total > 0) {
    return { tone: 'danger' as const, label: t('monitor.health.requestLog.checkpointAbnormal') }
  }
  return { tone: 'success' as const, label: t('monitor.health.requestLog.checkpointNormal') }
})

const metrics = computed(() => [
  {
    key: 'queue',
    label: t('monitor.health.requestLog.queue'),
    value: `${n(props.stats.queue_depth)} / ${n(props.stats.queue_capacity)}`,
    danger: false,
  },
  {
    key: 'enqueued',
    label: t('monitor.health.requestLog.enqueued'),
    value: n(props.stats.enqueued_total),
    danger: false,
  },
  {
    key: 'persisted',
    label: t('monitor.health.requestLog.persisted'),
    value: n(props.stats.persisted_total),
    danger: false,
  },
  {
    key: 'dropped',
    label: t('monitor.health.requestLog.droppedTotal'),
    value: n(props.stats.dropped_total),
    danger: props.stats.dropped_total > 0,
  },
])
</script>

<template>
  <section class="request-log-health" aria-labelledby="request-log-health-title">
    <MonitorSectionHeading
      id="request-log-health-title"
      :title="t('monitor.health.requestLog.title')"
      :description="t('monitor.health.requestLog.description')"
    />

    <Surface class="request-log-health__card" :padded="false">
      <header class="request-log-health__status">
        <StatusBadge :tone="state.tone" size="compact">{{ state.label }}</StatusBadge>
        <span class="request-log-health__failures">
          {{ t('monitor.health.requestLog.writeFailures') }}
          <strong
            :class="{ 'request-log-health__failures--danger': stats.write_failure_total > 0 }"
          >
            {{ n(stats.write_failure_total) }}
          </strong>
        </span>
      </header>

      <div class="request-log-health__checkpoint">
        <StatusBadge :tone="checkpointState.tone" size="compact">
          {{ checkpointState.label }}
        </StatusBadge>
        <span class="request-log-health__checkpoint-detail">
          {{ t('monitor.health.requestLog.checkpointFailures') }}
          <strong
            :class="{
              'request-log-health__failures--danger':
                stats.access_quota_checkpoint_write_failure_total > 0,
            }"
          >
            {{ n(stats.access_quota_checkpoint_write_failure_total) }}
          </strong>
          <template v-if="stats.last_access_quota_checkpoint_write_failure_at_ms !== null">
            · {{ t('monitor.health.requestLog.lastCheckpointFailureAt') }}
            <AppRelativeTime
              :instant="stats.last_access_quota_checkpoint_write_failure_at_ms"
              :locale="locale"
              empty-label="—"
              hint
            />
          </template>
        </span>
        <p
          v-if="stats.access_quota_checkpoint_write_failure_total > 0"
          class="request-log-health__checkpoint-risk"
        >
          {{ t('monitor.health.requestLog.checkpointRisk') }}
        </p>
      </div>

      <dl class="request-log-health__metrics">
        <div v-for="metric in metrics" :key="metric.key" class="request-log-health__metric">
          <dt>{{ metric.label }}</dt>
          <dd :class="{ 'request-log-health__metric-value--danger': metric.danger }">
            {{ metric.value }}
          </dd>
        </div>
      </dl>
    </Surface>
  </section>
</template>

<style scoped>
.request-log-health,
.request-log-health__card {
  display: grid;
  min-width: 0;
}

.request-log-health {
  grid-template-rows: auto var(--health-focus-content-height, 266px);
  gap: var(--space-4);
}

.request-log-health__card {
  height: 100%;
  grid-template-rows: auto auto minmax(0, 1fr);
  overflow: hidden;
}

.request-log-health__status {
  display: flex;
  min-height: 52px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  background: color-mix(in srgb, var(--color-surface-sunken) 48%, var(--color-surface));
  padding: 10px 14px;
}

.request-log-health__failures {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  white-space: nowrap;
}

.request-log-health__failures strong {
  color: var(--color-text);
  font-family: var(--font-mono);
  font-weight: 600;
}

.request-log-health__failures--danger {
  color: var(--color-danger);
}

.request-log-health__checkpoint {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 8px 14px;
}

.request-log-health__checkpoint-detail {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.request-log-health__checkpoint-detail strong {
  color: var(--color-text);
  font-family: var(--font-mono);
  font-weight: 600;
}

.request-log-health__checkpoint-risk {
  min-width: 0;
  margin: 0 0 0 auto;
  color: var(--color-danger);
  font-size: var(--text-xs);
  line-height: 1.35;
  text-align: right;
}

.request-log-health__metrics {
  display: grid;
  min-height: 0;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  grid-template-rows: repeat(2, minmax(68px, 1fr));
  margin: 0;
  background: var(--color-surface);
}

.request-log-health__metric {
  display: grid;
  min-width: 0;
  align-content: center;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
  border-left: 1px solid var(--color-border-subtle);
  padding: 10px 12px;
}

.request-log-health__metric:nth-child(-n + 2) {
  border-top: 0;
}

.request-log-health__metric:nth-child(odd) {
  border-left: 0;
}

.request-log-health__metric dt {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.request-log-health__metric dd {
  margin: 0;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: 22px;
  font-variant-numeric: tabular-nums;
  font-weight: 560;
  letter-spacing: -0.02em;
  line-height: 1;
  overflow-wrap: anywhere;
}

.request-log-health__metric .request-log-health__metric-value--danger {
  color: var(--color-danger);
}

@media (max-width: 1099px) and (min-width: 761px) {
  .request-log-health__metrics {
    grid-template-columns: repeat(4, minmax(0, 1fr));
    grid-template-rows: minmax(72px, auto);
  }

  .request-log-health__metric:nth-child(n) {
    border-top: 0;
    border-left: 1px solid var(--color-border-subtle);
  }

  .request-log-health__metric:first-child {
    border-left: 0;
  }
}

@media (max-width: 1099px) {
  .request-log-health {
    grid-template-rows: auto auto;
  }

  .request-log-health__card {
    height: auto;
  }
}

@media (max-width: 760px) {
  .request-log-health__status,
  .request-log-health__checkpoint {
    align-items: flex-start;
    flex-direction: column;
  }

  .request-log-health__checkpoint-risk {
    margin-left: 0;
    text-align: left;
  }

  .request-log-health__metric {
    min-height: 76px;
  }
}
</style>
