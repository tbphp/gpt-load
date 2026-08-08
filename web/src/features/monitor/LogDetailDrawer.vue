<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import {
  requestLogDetailQueryOptions,
  type RequestLogAttemptDto,
  type RequestLogPricingLineDto,
} from '@/app/resources/request-logs'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatEstimatedCost } from '@/lib/format'

import { formatCacheHitRate } from '@/lib/cache-rate'
import {
  formatLogDuration,
  formatLogOutputRate,
  formatLogReasoningBudget,
  formatLogTokenCount,
  reasoningBudgetSemantic,
  requestLogCostDisplayState,
  requestLogUsageDisplayState,
} from './log-format'

const props = defineProps<{ open: boolean; requestId: string | undefined }>()
defineEmits<{ 'update:open': [open: boolean] }>()
const client = useApiClient()
const { locale, t } = useI18n()
const query = useQuery(requestLogDetailQueryOptions(client, () => props.requestId))
const initialLoading = useStableLoading(() => props.open && query.isPending.value)
const log = computed(() => query.data.value)
const usageDisplayState = computed(() =>
  log.value ? requestLogUsageDisplayState(log.value) : 'not_applicable',
)
const costDisplayState = computed(() =>
  log.value ? requestLogCostDisplayState(log.value) : 'not_applicable',
)
const receipt = computed(
  () => log.value?.attempts.find((attempt) => attempt.pricing_receipt)?.pricing_receipt,
)
const cacheRows = computed(() => {
  if (!log.value || usageDisplayState.value !== 'reported') return []
  return [
    { label: t('monitor.logs.tokens.cacheRead'), value: log.value.cache_read_tokens },
    { label: t('monitor.logs.tokens.cacheWrite5m'), value: log.value.cache_write_5m_tokens },
    { label: t('monitor.logs.tokens.cacheWrite1h'), value: log.value.cache_write_1h_tokens },
    { label: t('monitor.logs.tokens.cacheWrite'), value: log.value.cache_write_unknown_tokens },
  ].filter(({ value }) => value !== '0')
})
const cacheRateLabel = computed(() => {
  if (!log.value || cacheRows.value.length === 0) return '—'
  return formatCacheHitRate(log.value.cache_read_tokens, log.value.input_tokens, locale.value)
})
const formula = computed(() => {
  const lines = receipt.value?.line_items ?? []
  const input = lines
    .filter((line) => line.code !== 'output')
    .map(formatFormulaLine)
    .join(' + ')
  const output = lines
    .filter((line) => line.code === 'output')
    .map(formatFormulaLine)
    .join(' + ')
  return {
    input: input || '—',
    output: output || '—',
  }
})

const usageStateLabel = computed(() => {
  if (!log.value) return '—'
  return t(`monitor.logs.drawer.usage.state.${log.value.usage_state}`)
})

const costStateLabel = computed(() => {
  const state = costDisplayState.value
  if (state === 'complete') return t('monitor.logs.drawer.usage.costState.priced')
  if (state === 'partial') return t('monitor.logs.drawer.usage.costState.partialPriced')
  return t(`monitor.logs.drawer.usage.costState.${state}`)
})

const costAmountLabel = computed(() => {
  if (!log.value) return '—'
  const state = costDisplayState.value
  if (state === 'complete') {
    return formatEstimatedCost(log.value.estimated_cost_nano_usd, locale.value)
  }
  if (state === 'partial') {
    return t('monitor.logs.cost.knownSubtotal', {
      cost: formatEstimatedCost(log.value.estimated_cost_nano_usd, locale.value),
    })
  }
  if (state === 'unpriced') return t('monitor.logs.cost.unpriced')
  return t('monitor.logs.cost.not_applicable')
})

function statusTone(status: string): 'success' | 'danger' | 'warning' | 'neutral' {
  if (status === 'success') return 'success'
  if (status === 'error') return 'danger'
  if (status === 'incomplete') return 'warning'
  return 'neutral'
}

function attemptTone(attempt: RequestLogAttemptDto): 'success' | 'danger' | 'warning' {
  if (attempt.failure_category === 'ok') return 'success'
  return attempt.will_retry ? 'warning' : 'danger'
}

function formatFormulaLine(line: RequestLogPricingLineDto): string {
  const quantity = formatLogTokenCount(line.quantity, locale.value)
  if (line.state === 'unpriced' || line.rate_nano_usd_per_million === null) {
    return `${quantity} × —`
  }
  const multiplier =
    line.multiplier.numerator === line.multiplier.denominator
      ? ''
      : ` × ${line.multiplier.numerator}/${line.multiplier.denominator}`
  return `${quantity} × ${formatEstimatedCost(line.rate_nano_usd_per_million, locale.value)}/1M${multiplier}`
}

function accessKeyLabel(): string {
  const key = log.value?.access_key
  if (!key) return '—'
  if (key.deleted) return t('monitor.logs.accessKey.deleted', { id: key.id })
  return key.name ? `${key.name} · #${key.id}` : `#${key.id}`
}

function groupLabel(): string {
  const groupID = log.value?.group_id
  if (groupID === null || groupID === undefined) return '—'
  const attempt = [...(log.value?.attempts ?? [])]
    .reverse()
    .find(({ group_id }) => group_id === groupID)
  return attempt ? `${attempt.group_name} · #${groupID}` : `#${groupID}`
}
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="t('monitor.logs.drawer.title')"
    :description="t('monitor.logs.drawer.description')"
    :close-label="t('monitor.logs.drawer.close')"
    @update:open="$emit('update:open', $event)"
  >
    <SkeletonSurface
      v-if="(open && query.isPending.value) || initialLoading"
      variant="detail"
      min-height="660px"
      :concealed="!initialLoading"
      :label="t('monitor.logs.drawer.loading')"
    />
    <QueryFeedback
      v-else-if="query.isError.value || !log"
      state="error"
      :message="t('monitor.logs.drawer.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="query.refetch()"
    />
    <div v-else class="log-detail">
      <header class="log-detail__summary">
        <StatusBadge :tone="statusTone(log.status)" size="compact">
          {{ t(`monitor.logs.status.${log.status}`)
          }}<template v-if="log.status !== 'success' && log.status_code">
            · {{ log.status_code }}</template
          >
        </StatusBadge>
        <span class="log-detail__time">
          <AppDateTime :instant="log.completed_at_ms" :locale="locale" precision="second" />
        </span>
        <span class="log-detail__request-id">
          <code>{{ log.request_id }}</code>
          <CopyButton
            :value="log.request_id"
            :label="t('monitor.logs.drawer.copyRequestId')"
            :success-label="t('common.copied')"
            :failure-label="t('common.copyFailed')"
          />
        </span>
      </header>

      <section class="log-detail__section">
        <h3>{{ t('monitor.logs.drawer.summary') }}</h3>
        <dl class="log-detail__grid">
          <div>
            <dt>{{ t('monitor.logs.drawer.status') }}</dt>
            <dd>
              {{ t(`monitor.logs.status.${log.status}`)
              }}<template v-if="log.status_code"> · {{ log.status_code }}</template>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.attemptCount') }}</dt>
            <dd>{{ log.attempt_count }}</dd>
          </div>
          <div v-if="log.stream">
            <dt>{{ t('monitor.logs.drawer.firstResponse') }}</dt>
            <dd>
              {{ log.first_response_ms === null ? '—' : formatLogDuration(log.first_response_ms) }}
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.duration') }}</dt>
            <dd>{{ formatLogDuration(log.duration_ms) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.outputRate') }}</dt>
            <dd>{{ formatLogOutputRate(log, locale) }}</dd>
          </div>
          <div v-if="log.error_code">
            <dt>{{ t('monitor.logs.drawer.errorCode') }}</dt>
            <dd>
              <code>{{ log.error_code }}</code>
            </dd>
          </div>
          <div v-if="log.error_summary" class="log-detail__wide">
            <dt>{{ t('monitor.logs.drawer.errorSummary') }}</dt>
            <dd>{{ log.error_summary }}</dd>
          </div>
        </dl>
      </section>

      <section class="log-detail__section">
        <h3>{{ t('monitor.logs.drawer.route') }}</h3>
        <dl class="log-detail__grid">
          <div>
            <dt>{{ t('monitor.logs.drawer.accessKey') }}</dt>
            <dd>{{ accessKeyLabel() }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.protocol') }}</dt>
            <dd>{{ log.protocol }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.clientModel') }}</dt>
            <dd>
              <code>{{ log.client_model ?? '—' }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
            <dd>
              <code>{{ log.upstream_model ?? '—' }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.group') }}</dt>
            <dd>{{ groupLabel() }}</dd>
          </div>
          <template v-if="log.reasoning">
            <div v-if="log.reasoning.mode">
              <dt>{{ t('monitor.logs.drawer.reasoningMode') }}</dt>
              <dd>
                <code>{{ log.reasoning.mode }}</code>
              </dd>
            </div>
            <div v-if="log.reasoning.effort">
              <dt>{{ t('monitor.logs.drawer.reasoningEffort') }}</dt>
              <dd>
                <code>{{ log.reasoning.effort }}</code>
              </dd>
            </div>
            <div v-if="log.reasoning.budget_tokens !== null">
              <dt>{{ t('monitor.logs.drawer.reasoningBudget') }}</dt>
              <dd v-if="reasoningBudgetSemantic(log.reasoning.budget_tokens) === 'dynamic'">
                {{ t('monitor.logs.drawer.reasoningBudgetDynamic') }}
              </dd>
              <dd v-else-if="reasoningBudgetSemantic(log.reasoning.budget_tokens) === 'disabled'">
                {{ t('monitor.logs.drawer.reasoningBudgetDisabled') }}
              </dd>
              <dd v-else>
                {{
                  t('monitor.logs.drawer.reasoningBudgetValue', {
                    value: formatLogReasoningBudget(log.reasoning.budget_tokens, locale),
                  })
                }}
              </dd>
            </div>
          </template>
          <div v-else>
            <dt>{{ t('monitor.logs.drawer.reasoningConfig') }}</dt>
            <dd>{{ t('monitor.logs.drawer.reasoningNotSpecified') }}</dd>
          </div>
        </dl>
      </section>

      <section class="log-detail__section">
        <h3>{{ t('monitor.logs.drawer.usage.title') }}</h3>
        <dl class="log-detail__grid">
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.usageStateLabel') }}</dt>
            <dd>{{ usageStateLabel }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.costStateLabel') }}</dt>
            <dd>{{ costStateLabel }}</dd>
          </div>
          <div v-if="usageDisplayState === 'reported'">
            <dt>{{ t('monitor.logs.tokens.input') }}</dt>
            <dd>{{ formatLogTokenCount(log.input_tokens, locale) }}</dd>
          </div>
          <div v-if="usageDisplayState === 'reported'">
            <dt>{{ t('monitor.logs.tokens.output') }}</dt>
            <dd>{{ formatLogTokenCount(log.output_tokens, locale) }}</dd>
          </div>
          <div v-for="row in cacheRows" :key="row.label">
            <dt>{{ row.label }}</dt>
            <dd>{{ formatLogTokenCount(row.value, locale) }}</dd>
          </div>
          <div v-if="cacheRows.length > 0">
            <dt>{{ t('monitor.logs.tokens.cacheHitRate') }}</dt>
            <dd>{{ cacheRateLabel }}</dd>
          </div>
          <div v-if="costDisplayState !== 'unpriced'">
            <dt>{{ t('monitor.logs.drawer.usage.estimatedCost') }}</dt>
            <dd>{{ costAmountLabel }}</dd>
          </div>
          <div
            v-if="costDisplayState !== 'unpriced' && receipt && usageDisplayState === 'reported'"
            class="log-detail__wide"
          >
            <dt>{{ t('monitor.logs.receipt.formula') }}</dt>
            <dd class="log-detail__formula">
              <span>{{ t('monitor.logs.receipt.input') }} = {{ formula.input }}</span>
              <span>{{ t('monitor.logs.receipt.output') }} = {{ formula.output }}</span>
            </dd>
          </div>
        </dl>
      </section>

      <section class="log-detail__section">
        <h3>{{ t('monitor.logs.drawer.attempts') }}</h3>
        <p v-if="log.attempts.length === 0" class="log-detail__empty">
          {{ t('monitor.logs.drawer.noAttempts') }}
        </p>
        <article v-for="attempt in log.attempts" :key="attempt.sequence" class="log-attempt">
          <header>
            <span>{{ t('monitor.logs.drawer.attempt', { sequence: attempt.sequence }) }}</span>
            <StatusBadge :tone="attemptTone(attempt)" size="compact">
              {{ t(`monitor.logs.failureCategory.${attempt.failure_category}`)
              }}<template v-if="attempt.status_code"> · {{ attempt.status_code }}</template>
            </StatusBadge>
          </header>
          <dl class="log-detail__grid">
            <div>
              <dt>{{ t('monitor.logs.drawer.group') }}</dt>
              <dd>{{ attempt.group_name }} · #{{ attempt.group_id }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.upstreamKey') }}</dt>
              <dd>#{{ attempt.key_id }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
              <dd>
                <code>{{ attempt.upstream_model ?? '—' }}</code>
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.duration') }}</dt>
              <dd>{{ formatLogDuration(attempt.duration_ms) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.action') }}</dt>
              <dd>{{ t(`monitor.logs.action.${attempt.action}`) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.willRetry') }}</dt>
              <dd>{{ attempt.will_retry ? t('monitor.logs.yes') : t('monitor.logs.no') }}</dd>
            </div>
            <div v-if="attempt.error_code">
              <dt>{{ t('monitor.logs.drawer.errorCode') }}</dt>
              <dd>
                <code>{{ attempt.error_code }}</code>
              </dd>
            </div>
            <div v-if="attempt.error_summary" class="log-detail__wide">
              <dt>{{ t('monitor.logs.drawer.errorSummary') }}</dt>
              <dd>{{ attempt.error_summary }}</dd>
            </div>
          </dl>
        </article>
      </section>
    </div>
  </AppDrawer>
</template>

<style scoped>
.log-detail {
  display: grid;
  min-width: 0;
}

.log-detail__summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  padding: 16px 0;
}

.log-detail__time {
  margin-left: auto;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}

.log-detail__request-id {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 6px;
}

.log-detail__request-id code {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log-detail__request-id :deep(.copy-control button) {
  width: 28px;
  height: 28px;
  border-color: transparent;
}

.log-detail__section {
  border-top: 1px solid var(--color-border-subtle);
  padding: 16px 0;
}

.log-detail__section h3 {
  margin: 0 0 12px;
  font-size: var(--text-sm);
  font-weight: 650;
}

.log-detail__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 18px;
  margin: 0;
}

.log-detail__grid > div {
  min-width: 0;
}

.log-detail__grid dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.log-detail__grid dd {
  margin: 3px 0 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.log-detail__wide {
  grid-column: 1 / -1;
}

.log-detail__formula {
  display: grid;
  gap: 4px;
  font-family: var(--font-mono);
  line-height: 1.6;
}

.log-attempt + .log-attempt {
  border-top: 1px solid var(--color-border-subtle);
}

.log-attempt {
  padding: 13px 0;
}

.log-attempt > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 12px;
  color: var(--color-text);
  font-size: var(--text-sm);
}

.log-detail__empty {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.log-detail :deep(.status-badge) {
  font-weight: 400;
}

@media (max-width: 520px) {
  .log-detail__grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .log-detail__wide {
    grid-column: auto;
  }
}
</style>
