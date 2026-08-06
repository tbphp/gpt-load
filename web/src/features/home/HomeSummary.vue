<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HomeBaseDto, HomeRange } from '@/app/resources/home'
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
import { formatCacheHitRate } from '@/lib/cache-rate'

import type { HomeStatisticsState } from './home-presenter'
import { homeRangeLabelKey } from './home-range'

const props = withDefaults(
  defineProps<{
    base: HomeBaseDto
    statisticsState: HomeStatisticsState
    selectedRange: HomeRange
    observedAtMs: number | null
    uptimeNowMs: number
    loading?: boolean
  }>(),
  {
    loading: false,
  },
)

const emit = defineEmits<{ selectRange: [range: HomeRange] }>()
const { locale, t } = useI18n()

const snapshot = computed(() => {
  const state = props.statisticsState
  return state.kind === 'initial' ? null : state.snapshot
})
const rangeOptions = computed(() => [
  { value: '24h', label: t('home.range.last24Hours') },
  { value: '30d', label: t('home.range.last30Days') },
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
const cacheDetail = computed(() => {
  const summary = snapshot.value?.summary
  if (!summary) return ''
  return t('home.ledger.cacheTokens', {
    read: formatTokens(summary.cache_read_tokens, locale.value),
    input: formatTokens(summary.input_tokens, locale.value),
  })
})
const costDetailExact = computed(() => {
  const summary = snapshot.value?.summary
  if (!summary) return ''
  const tokens = t('home.ledger.tokens', {
    count: formatInteger(summary.total_tokens, locale.value),
  })
  return summary.unpriced_request_count === 0
    ? tokens
    : t('home.ledger.tokensWithUnpriced', {
        tokens,
        unpriced: formatInteger(summary.unpriced_request_count, locale.value),
      })
})
function rangeLabel(range: HomeRange): string {
  return t(homeRangeLabelKey(range))
}
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

  <section class="home-summary__figures" :aria-busy="loading ? 'true' : undefined">
    <template v-if="loading || !snapshot">
      <div class="home-summary__figure home-summary__figure--loading">
        <SkeletonBlock width="48%" height="0.72rem" />
        <SkeletonBlock width="62%" height="2.25rem" />
        <SkeletonBlock width="54%" height="0.75rem" />
      </div>
      <div
        class="home-summary__figure home-summary__figure--secondary home-summary__figure--loading"
      >
        <SkeletonBlock width="52%" height="0.72rem" />
        <SkeletonBlock width="68%" height="2.25rem" />
        <SkeletonBlock width="58%" height="0.75rem" />
      </div>
      <div
        class="home-summary__figure home-summary__figure--secondary home-summary__figure--loading"
      >
        <SkeletonBlock width="52%" height="0.72rem" />
        <SkeletonBlock width="68%" height="2.25rem" />
        <SkeletonBlock width="58%" height="0.75rem" />
      </div>
    </template>
    <template v-else>
      <StatFigure
        class="home-summary__figure"
        :label="t('home.ledger.successRate', { range: rangeLabel(snapshot.range) })"
        :value="
          formatPercent(snapshot.summary.success_count, snapshot.summary.request_count, locale)
        "
        :detail="successDetail"
      />
      <StatFigure
        class="home-summary__figure home-summary__figure--secondary"
        :label="t('home.ledger.cacheHitRate', { range: rangeLabel(snapshot.range) })"
        :value="
          formatCacheHitRate(
            snapshot.summary.cache_read_tokens,
            snapshot.summary.input_tokens,
            locale,
          )
        "
        :detail="cacheDetail"
      />
      <StatFigure
        class="home-summary__figure home-summary__figure--secondary"
        :label="t('home.ledger.estimatedCost', { range: rangeLabel(snapshot.range) })"
        :value="formatEstimatedCost(snapshot.summary.estimated_cost_nano_usd, locale)"
        :detail="costDetail"
        :detail-title="costDetailExact"
        :detail-aria-label="costDetailExact"
      />
    </template>
    <SegmentedControl
      class="home-summary__range"
      :model-value="selectedRange"
      :label="t('home.range.label')"
      :options="rangeOptions"
      size="compact"
      @update:model-value="selectRange"
    />
  </section>
</template>

<style scoped>
.home-summary__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 22px;
  flex-wrap: wrap;
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: 20px;
}

.home-summary__facts {
  max-width: none;
  margin: 0;
  color: var(--color-text-muted);
  font-family: var(--font-serif);
  font-size: var(--title-lede);
  font-weight: 500;
  line-height: var(--line-compact);
  letter-spacing: -0.015em;
}

.home-summary__fact {
  color: var(--color-text-muted);
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
  grid-template-columns: max-content max-content;
  gap: 5px 1ch;
  margin: 0;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: var(--line-compact);
  white-space: nowrap;
}

.home-summary__stamp div {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: baseline;
}
.home-summary__stamp dt,
.home-summary__stamp dd {
  margin: 0;
}
.home-summary__stamp dt {
  text-align: right;
}
.home-summary__stamp dd {
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  text-align: left;
}

.home-summary__figures {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr)) auto;
  align-items: start;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 24px;
}
.home-summary__range {
  flex: none;
  grid-column: 4;
}
.home-summary__figure {
  border-left: 1px solid var(--color-border-subtle);
  padding: 0 28px;
}
.home-summary__figure:first-child {
  border-left: 0;
  padding-left: 0;
}
.home-summary__figure--secondary {
  border-left: 1px solid var(--color-border-subtle);
}
.home-summary__figure--loading {
  display: grid;
  min-height: 5.5rem;
  align-content: start;
  gap: 6px;
}

@media (max-width: 860px) {
  .home-summary__header {
    align-items: start;
  }
  .home-summary__stamp {
    justify-content: start;
  }
  .home-summary__figures {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .home-summary__range {
    grid-column: 1 / -1;
    grid-row: 1;
    justify-self: end;
    width: max-content;
    max-width: 100%;
    margin-bottom: 14px;
  }
  .home-summary__figure {
    grid-row: 2;
    padding: 0 18px;
  }
  .home-summary__figure:first-child {
    padding-left: 0;
  }
}

@media (max-width: 640px) {
  .home-summary__figures {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .home-summary__figure:nth-child(3) {
    grid-column: 1 / -1;
    grid-row: 3;
    border-left: 0;
    padding: 16px 0 0;
  }
}
</style>
