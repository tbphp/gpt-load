<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HomeBaseDto, HomeRange } from '@/app/resources/home'
import PageSection from '@/components/layout/PageSection.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'
import StatFigure from '@/components/ui/StatFigure.vue'
import {
  formatDuration,
  formatEstimatedCost,
  formatInteger,
  formatLocalInstant,
  formatLocalTime,
  formatPercent,
  formatTokens,
} from '@/lib/format'

import type { HomeStatisticsState } from './home-presenter'

const props = defineProps<{
  base: HomeBaseDto
  statisticsState: HomeStatisticsState
  selectedRange: HomeRange
  targetRange: HomeRange | null
  observedAtMs: number | null
  uptimeNowMs: number
}>()

const emit = defineEmits<{ selectRange: [range: HomeRange] }>()
const { locale, t } = useI18n()

const snapshot = computed(() => {
  const state = props.statisticsState
  return state.kind === 'initial' ? null : state.snapshot
})
const isSwitching = computed(() => props.statisticsState.kind === 'switching')
const rangeOptions = computed(() => [
  { value: '24h', label: t('home.range.last24Hours'), disabled: props.targetRange === '24h' },
  { value: '30d', label: t('home.range.last30Days'), disabled: props.targetRange === '30d' },
])
const updated = computed(() =>
  props.observedAtMs === null ? '—' : formatLocalTime(props.observedAtMs, locale.value),
)
const updatedTitle = computed(() =>
  props.observedAtMs === null ? undefined : formatLocalInstant(props.observedAtMs, locale.value),
)
const successDetail = computed(() => {
  const summary = snapshot.value?.summary
  if (!summary) return ''
  const requests = t('home.ledger.requests', {
    count: formatInteger(summary.request_count, locale.value),
  })
  return summary.failure_count === 0
    ? requests
    : t('home.ledger.requestsWithFailures', {
        requests,
        failures: formatInteger(summary.failure_count, locale.value),
      })
})
const costDetail = computed(() => {
  const summary = snapshot.value?.summary
  if (!summary) return ''
  const tokens = t('home.ledger.tokens', {
    count: formatTokens(summary.total_tokens, locale.value),
  })
  return summary.unpriced_request_count === 0
    ? tokens
    : t('home.ledger.tokensWithUnpriced', {
        tokens,
        unpriced: formatInteger(summary.unpriced_request_count, locale.value),
      })
})
function selectRange(value: string): void {
  if (value === '24h' || value === '30d') emit('selectRange', value)
}
</script>

<template>
  <header class="home-summary__header">
    <div>
      <h1 id="home-title" class="home-summary__facts">
        <span class="home-summary__fact">
          <strong>{{ formatInteger(base.inventory.group_count, locale) }}</strong>
          <span>{{ t('home.ledger.factGroups') }}</span>
        </span>
        <span class="home-summary__separator" aria-hidden="true"> · </span>
        <span class="home-summary__fact">
          <strong>
            {{ formatInteger(base.inventory.available_upstream_key_count, locale) }}/{{
              formatInteger(base.inventory.upstream_key_count, locale)
            }}
          </strong>
          <span>{{ t('home.ledger.factAvailableKeys') }}</span>
        </span>
        <span class="home-summary__separator" aria-hidden="true"> · </span>
        <span class="home-summary__fact">
          <strong>{{ formatInteger(base.inventory.model_count, locale) }}</strong>
          <span>{{ t('home.ledger.factModels') }}</span>
        </span>
      </h1>
    </div>
    <dl class="home-summary__stamp">
      <div>
        <dt>{{ t('home.ledger.updated') }}</dt>
        <dd :title="updatedTitle">{{ updated }}</dd>
      </div>
      <div>
        <dt>{{ t('home.ledger.version') }}</dt>
        <dd>{{ base.version }}</dd>
      </div>
      <div>
        <dt>{{ t('home.ledger.uptime') }}</dt>
        <dd>{{ formatDuration(base.started_at_ms, uptimeNowMs, locale) }}</dd>
      </div>
    </dl>
  </header>

  <PageSection class="home-summary__figures" :divided="true">
    <template #actions>
      <SegmentedControl
        class="home-summary__range"
        :model-value="selectedRange"
        :label="t('home.range.label')"
        :options="rangeOptions"
        @update:model-value="selectRange"
      />
    </template>
    <div
      v-if="isSwitching || !snapshot"
      class="home-summary__skeletons"
      :aria-label="t('home.ledger.statisticsLoading')"
    >
      <SkeletonBlock height="5.5rem" />
      <SkeletonBlock height="5.5rem" />
    </div>
    <div v-else class="home-summary__stats">
      <StatFigure
        :label="t('home.ledger.successRate')"
        :value="
          formatPercent(snapshot.summary.success_count, snapshot.summary.request_count, locale)
        "
        :detail="successDetail"
      />
      <StatFigure
        :label="t('home.ledger.estimatedCost')"
        :value="formatEstimatedCost(snapshot.summary.estimated_cost_nano_usd, locale)"
        :detail="costDetail"
      />
    </div>
  </PageSection>
</template>

<style scoped>
.home-summary__header {
  display: flex;
  align-items: start;
  justify-content: space-between;
  gap: var(--space-5);
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: var(--space-5);
}

.home-summary__facts {
  margin: 0;
  color: var(--color-text-muted);
  font-family: var(--font-serif);
  font-size: var(--title-lede);
  font-weight: 500;
  line-height: var(--line-compact);
}

.home-summary__fact {
  white-space: nowrap;
}

.home-summary__fact strong {
  color: var(--color-text);
  font-weight: 650;
}

.home-summary__fact > span,
.home-summary__separator {
  color: var(--color-text-muted);
  font-weight: 500;
}

.home-summary__stamp {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  text-align: right;
  white-space: nowrap;
}

.home-summary__stamp div {
  display: flex;
  justify-content: end;
  gap: var(--space-2);
}
.home-summary__stamp dt,
.home-summary__stamp dd {
  margin: 0;
}
.home-summary__stamp dd {
  color: var(--color-text-muted);
}

.home-summary__figures {
  position: relative;
}
.home-summary__figures :deep(.page-section__header) {
  justify-content: flex-end;
}
.home-summary__range {
  flex: none;
}
.home-summary__stats,
.home-summary__skeletons {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  min-height: 5.5rem;
}
.home-summary__stats > :nth-child(2) {
  border-left: 1px solid var(--color-border-subtle);
  padding-left: var(--space-7);
}
.home-summary__skeletons {
  gap: var(--space-7);
}

@media (max-width: 860px) {
  .home-summary__header {
    align-items: start;
    flex-direction: column;
  }
  .home-summary__stamp {
    text-align: left;
  }
  .home-summary__stamp div {
    justify-content: start;
  }
  .home-summary__figures :deep(.page-section__content) {
    width: 100%;
  }
  .home-summary__stats > :nth-child(2) {
    padding-left: var(--space-4);
  }
}
</style>
