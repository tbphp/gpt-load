<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Clock3, KeyRound, Layers2, ShieldAlert } from '@lucide/vue'
import type { HealthReport } from '@modern/api/health'
import { AppButton, AppIcon, AppSegmentedBar } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import type { HealthKind } from './health-state'

const props = defineProps<{ report: HealthReport; unavailable: number }>()
defineEmits<{ select: [kind: HealthKind] }>()
const { t, locale } = useI18n()
const count = (value: number) => formatCompactNumber(value, locale.value)
const segments = computed(() => [
  { key: 'available', value: props.report.counts.available, tone: 'success' as const },
  { key: 'cooldown', value: props.report.counts.cooldown, tone: 'warning' as const },
  { key: 'isolated', value: props.report.counts.blacklisted, tone: 'danger' as const },
])
const hint = computed(() =>
  segments.value.map((item) => `${t('health.' + item.key)} ${count(item.value)}`).join(' · '),
)
const metrics = computed(() => [
  {
    kind: 'cooldown' as const,
    label: 'cooldown',
    icon: Clock3,
    value: props.report.counts.cooldown,
    tone: 'warning',
  },
  {
    kind: 'isolated' as const,
    label: 'isolated',
    icon: ShieldAlert,
    value: props.report.counts.blacklisted,
    tone: 'danger',
  },
  {
    kind: 'group' as const,
    label: 'unavailable',
    icon: Layers2,
    value: props.unavailable,
    tone: 'danger',
  },
  {
    kind: 'access_key' as const,
    label: 'blocked',
    icon: KeyRound,
    value: props.report.accessKeys.length,
    tone: 'danger',
  },
])
</script>

<template>
  <section class="modern-health-overview" :aria-label="t('health.overview')">
    <div class="modern-health-availability">
      <div class="modern-health-availability-value">
        <span>{{ t('health.available') }}</span>
        <div>
          <strong>{{ count(report.counts.available) }}</strong
          ><span>/ {{ count(report.counts.credentials) }}</span>
        </div>
      </div>
      <AppSegmentedBar :segments="segments" :label="hint" />
      <p>{{ t(report.counts.credentials ? 'health.scope' : 'health.emptyScope') }}</p>
    </div>
    <div v-for="metric in metrics" :key="metric.kind" class="modern-health-metric">
      <span class="modern-health-metric-label"
        ><AppIcon :icon="metric.icon" size="sm" />{{ t('health.' + metric.label) }}</span
      >
      <AppButton
        variant="text"
        class="modern-health-metric-value"
        :data-tone="metric.value ? metric.tone : undefined"
        @click="$emit('select', metric.kind)"
      >
        {{ count(metric.value) }}
      </AppButton>
    </div>
  </section>
</template>

<style scoped>
.modern-health-overview {
  display: grid;
  grid-template-columns: minmax(220px, 1.5fr) repeat(4, minmax(0, 1fr));
  flex: none;
  align-items: center;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  margin-top: var(--modern-space-5);
  padding: var(--modern-space-4) 0;
}
.modern-health-availability {
  display: grid;
  gap: var(--modern-space-2);
  padding-inline: var(--modern-space-5);
}
.modern-health-availability-value {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
.modern-health-availability-value > span {
  font-size: var(--modern-font-size-secondary);
  color: var(--modern-text);
}
.modern-health-availability-value > div {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-1-5);
  font-variant-numeric: tabular-nums;
}
.modern-health-availability-value strong {
  font-size: var(--modern-font-size-title);
  font-weight: var(--modern-weight-semibold);
  line-height: var(--modern-leading-title);
}
.modern-health-availability-value > div > span,
.modern-health-availability p {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-health-metric {
  border-left: var(--modern-line-width) solid var(--modern-border);
  padding-inline: var(--modern-space-5);
  display: grid;
  align-content: center;
  gap: var(--modern-space-2);
}
.modern-health-metric-label {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-health-metric-value {
  font-size: var(--modern-font-size-title);
  font-weight: var(--modern-weight-semibold);
  line-height: var(--modern-leading-title);
  font-variant-numeric: tabular-nums;
}
.modern-health-metric-value[data-tone='warning'] {
  color: var(--modern-warning);
}
.modern-health-metric-value[data-tone='danger'] {
  color: var(--modern-danger);
}
@media (max-width: 1150px) {
  .modern-health-overview {
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: var(--modern-space-4) 0;
  }
  .modern-health-availability {
    grid-column: 1 / -1;
  }
  .modern-health-metric:nth-child(2) {
    border-left: 0;
  }
}
@media (max-width: 760px) {
  .modern-health-metric {
    padding-inline: var(--modern-space-2);
  }
  .modern-health-metric-label {
    font-size: var(--modern-font-size-small);
    gap: var(--modern-space-1);
    flex-wrap: wrap;
  }
  .modern-health-availability {
    padding-inline: var(--modern-space-3);
  }
}
</style>
