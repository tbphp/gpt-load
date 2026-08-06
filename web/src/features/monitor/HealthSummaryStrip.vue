<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { KeyCounts } from '@/app/resources/health'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import type { StatusTone } from '@/components/ui/status-presenter'

interface CooldownRecovery {
  relative: string
  exact: string
}

interface HealthOverviewItem {
  key: string
  label: string
  value: string
  detail: string
  tooltip: string | undefined
  tone: StatusTone
}

const props = defineProps<{
  counts: KeyCounts
  earliestCooldown: CooldownRecovery | null
}>()

const { n, t } = useI18n()
const items = computed<HealthOverviewItem[]>(() => [
  {
    key: 'available',
    label: t('monitor.health.overview.available'),
    value: n(props.counts.available),
    detail: t('monitor.health.overview.total', { total: n(props.counts.total) }),
    tooltip: undefined,
    tone:
      props.counts.available > 0
        ? 'success'
        : props.counts.total === 0 || props.counts.disabled === props.counts.total
          ? 'neutral'
          : 'danger',
  },
  {
    key: 'cooldown',
    label: t('monitor.health.overview.cooldown'),
    value: n(props.counts.cooldown),
    detail:
      props.earliestCooldown === null
        ? t('monitor.health.overview.cooldownClear')
        : t('monitor.health.overview.cooldownRecovery', {
            time: props.earliestCooldown.relative,
          }),
    tooltip: props.earliestCooldown?.exact,
    tone: props.counts.cooldown > 0 ? 'warning' : 'neutral',
  },
  {
    key: 'blacklisted',
    label: t('monitor.health.overview.blacklisted'),
    value: n(props.counts.blacklisted),
    detail:
      props.counts.blacklisted > 0
        ? t('monitor.health.overview.blacklistedRecovery')
        : t('monitor.health.overview.blacklistedClear'),
    tooltip: undefined,
    tone: props.counts.blacklisted > 0 ? 'danger' : 'neutral',
  },
  {
    key: 'disabled',
    label: t('monitor.health.overview.disabled'),
    value: n(props.counts.disabled),
    detail: t('monitor.health.overview.disabledDescription'),
    tooltip: undefined,
    tone: 'neutral',
  },
])
</script>

<template>
  <section class="health-overview" :aria-label="t('monitor.health.overview.label')">
    <article
      v-for="item in items"
      :key="item.key"
      class="health-overview__item"
      :class="`health-overview__item--${item.tone}`"
    >
      <span class="health-overview__label">{{ item.label }}</span>
      <strong class="health-overview__value">{{ item.value }}</strong>
      <AppTooltip v-if="item.tooltip" :content="item.tooltip" side="bottom">
        <span class="health-overview__detail health-overview__detail--tooltip" tabindex="0">
          {{ item.detail }}
        </span>
      </AppTooltip>
      <span v-else class="health-overview__detail">{{ item.detail }}</span>
    </article>
  </section>
</template>

<style scoped>
.health-overview {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-border-subtle);
  gap: 1px;
}

.health-overview__item {
  --health-overview-color: var(--color-text);
  --health-overview-dot: var(--color-text-faint);

  display: grid;
  min-width: 0;
  min-height: 108px;
  align-content: start;
  gap: 0;
  background: var(--color-surface);
  padding: 16px 18px;
}

.health-overview__item--success {
  --health-overview-color: var(--color-success);
  --health-overview-dot: var(--color-success);
}

.health-overview__item--warning {
  --health-overview-color: var(--color-warning);
  --health-overview-dot: var(--color-warning);
}

.health-overview__item--danger {
  --health-overview-color: var(--color-danger);
  --health-overview-dot: var(--color-danger);
}

.health-overview__label {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

.health-overview__label::before {
  width: 6px;
  height: 6px;
  flex: 0 0 6px;
  border-radius: 50%;
  background: var(--health-overview-dot);
  content: '';
}

.health-overview__value {
  display: block;
  margin-top: 8px;
  color: var(--health-overview-color);
  font-family: var(--font-mono);
  font-size: clamp(1.45rem, 2.1vw, 1.9rem);
  font-variant-numeric: tabular-nums;
  font-weight: 580;
  letter-spacing: -0.045em;
  line-height: 1;
}

.health-overview__detail {
  min-width: 0;
  margin-top: 9px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.4;
  overflow-wrap: anywhere;
}

.health-overview__detail--tooltip {
  width: fit-content;
  border-radius: 3px;
}

@media (max-width: 900px) {
  .health-overview {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .health-overview__item {
    min-height: 104px;
  }
}

@media (max-width: 560px) {
  .health-overview {
    grid-template-columns: minmax(0, 1fr);
  }

  .health-overview__item {
    min-height: 94px;
  }
}
</style>
