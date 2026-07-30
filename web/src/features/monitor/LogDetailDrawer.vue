<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  FailureCategory,
  RequestLogAction,
  RequestLogItemDto,
  RequestLogStatus,
} from '@/app/resources/request-logs'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatEstimatedUSD } from '@/features/usage/estimated-cost'

const props = defineProps<{
  open: boolean
  log: RequestLogItemDto | null
}>()
defineEmits<{ 'update:open': [open: boolean] }>()
const { locale, t } = useI18n()

const failureCategories: readonly FailureCategory[] = [
  'ok',
  'rate_limited',
  'model_unavailable',
  'invalid_key',
  'upstream_host_error',
  'client_error',
  'downstream_cancel',
  'ambiguous',
]
const actions: readonly RequestLogAction[] = [
  'terminate',
  'retry',
  'cooldown_key',
  'fail_key',
  'skip_group',
]
const orderedAttempts = computed(() =>
  [...(props.log?.attempts ?? [])].sort((left, right) => left.sequence - right.sequence),
)
const inspectorTarget = computed(() => {
  if (
    !props.log?.protocol ||
    !Number.isSafeInteger(props.log.access_key.id) ||
    props.log.access_key.id <= 0
  ) {
    return null
  }
  const query: Record<string, string | number> = {
    tab: 'inspector',
    protocol: props.log.protocol,
  }
  if (props.log.client_model !== null) query.external_model = props.log.client_model
  query.access_key_id = props.log.access_key.id
  return {
    name: 'monitor',
    query,
  }
})
const usageTone = computed(() => {
  if (props.log?.usage_state === 'complete') return 'success'
  if (props.log?.usage_state === 'partial') return 'warning'
  if (props.log?.usage_state === 'missing') return 'danger'
  return 'neutral'
})
const costTone = computed(() => {
  if (props.log?.cost_state === 'priced') return 'success'
  if (props.log?.cost_state === 'unpriced') return 'warning'
  return 'neutral'
})

function statusTone(status: RequestLogStatus): 'success' | 'danger' | 'warning' | 'neutral' {
  if (status === 'success') return 'success'
  if (status === 'error') return 'danger'
  if (status === 'incomplete') return 'warning'
  return 'neutral'
}

function failureLabel(value: string): string {
  return failureCategories.includes(value as FailureCategory)
    ? t(`monitor.logs.failureCategory.${value}`)
    : t('monitor.logs.failureCategory.unknown')
}

function actionLabel(value: string): string {
  return actions.includes(value as RequestLogAction)
    ? t(`monitor.logs.action.${value}`)
    : t('monitor.logs.action.unknown')
}

function accessKeyLabel(log: RequestLogItemDto): string {
  if (log.access_key.deleted) {
    return t('monitor.logs.accessKey.deleted', { id: log.access_key.id })
  }
  if (log.access_key.name) {
    return t('monitor.logs.accessKey.named', {
      id: log.access_key.id,
      name: log.access_key.name,
    })
  }
  return `#${log.access_key.id}`
}

function protocolLabel(log: RequestLogItemDto): string {
  return log.protocol
    ? t(`common.protocols.${log.protocol}`)
    : t('monitor.logs.drawer.usage.unknown')
}

function modelLabel(model: string | null): string {
  return model ?? t('monitor.logs.drawer.modelNotSpecified')
}

function usageStateLabel(log: RequestLogItemDto): string {
  return t(`monitor.logs.drawer.usage.state.${log.usage_state}`)
}

function costStateLabel(log: RequestLogItemDto): string {
  if (log.cost_state === 'priced' && log.usage_state === 'partial') {
    return t('monitor.logs.drawer.usage.costState.partialPriced')
  }
  return t(`monitor.logs.drawer.usage.costState.${log.cost_state}`)
}

function aggregationLabel(log: RequestLogItemDto): string {
  if (log.usage_state === 'not_applicable') {
    return t('monitor.logs.drawer.usage.aggregation.notApplicable')
  }
  if (log.usage_state === 'missing') {
    return t('monitor.logs.drawer.usage.aggregation.missing')
  }
  if (log.usage_state === 'partial' && log.cost_state === 'priced') {
    return t('monitor.logs.drawer.usage.aggregation.partialPriced')
  }
  if (log.usage_state === 'partial') {
    return t('monitor.logs.drawer.usage.aggregation.partialUnpriced')
  }
  if (log.cost_state === 'unpriced') {
    return t('monitor.logs.drawer.usage.aggregation.completeUnpriced')
  }
  return t('monitor.logs.drawer.usage.aggregation.completePriced')
}

function tokenValue(log: RequestLogItemDto, value: number): string {
  if (log.usage_state === 'not_applicable') return t('monitor.logs.drawer.usage.notApplicable')
  if (log.usage_state === 'missing') return t('monitor.logs.drawer.usage.unknown')
  return new Intl.NumberFormat(locale.value).format(value)
}

function estimatedCost(log: RequestLogItemDto): string {
  if (log.cost_state === 'not_applicable') return t('monitor.logs.drawer.usage.notApplicable')
  if (log.cost_state === 'unpriced') return t('monitor.logs.drawer.usage.unknown')
  return formatEstimatedUSD(log.estimated_cost_usd, locale.value)
}
</script>

<template>
  <AppDrawer
    :open="open"
    :title="t('monitor.logs.drawer.title')"
    :description="t('monitor.logs.drawer.description')"
    :close-label="t('monitor.logs.drawer.close')"
    @update:open="$emit('update:open', $event)"
  >
    <template #trigger><slot name="trigger" /></template>

    <div v-if="log" class="log-detail">
      <section class="log-detail__section" aria-labelledby="log-detail-summary-heading">
        <h2 id="log-detail-summary-heading">{{ t('monitor.logs.drawer.summary') }}</h2>
        <dl class="log-detail__facts">
          <div class="log-detail__wide">
            <dt>{{ t('monitor.logs.drawer.requestId') }}</dt>
            <dd class="log-detail__copy">
              <code>{{ log.request_id }}</code>
              <CopyButton
                :value="log.request_id"
                :label="t('monitor.logs.drawer.copyRequestId')"
                :success-label="t('common.copied')"
                :failure-label="t('common.copyFailed')"
              />
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.completedAt') }}</dt>
            <dd>
              <time :datetime="log.completed_at">{{ log.completed_at }}</time>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.status') }}</dt>
            <dd>
              <StatusBadge :tone="statusTone(log.status)">
                {{ t(`monitor.logs.status.${log.status}`) }}
              </StatusBadge>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.protocol') }}</dt>
            <dd>{{ protocolLabel(log) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.accessKey') }}</dt>
            <dd>{{ accessKeyLabel(log) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.clientModel') }}</dt>
            <dd>
              <code>{{ modelLabel(log.client_model) }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
            <dd>
              <code>{{ modelLabel(log.upstream_model) }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.statusCode') }}</dt>
            <dd>{{ log.status_code }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.duration') }}</dt>
            <dd>{{ log.duration_ms }} ms</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.errorCode') }}</dt>
            <dd>
              <code>{{ log.error_code || t('monitor.logs.none') }}</code>
            </dd>
          </div>
        </dl>
        <div class="log-detail__summary">
          <h3>{{ t('monitor.logs.drawer.errorSummary') }}</h3>
          <p data-test="log-error-summary">{{ log.error_summary || t('monitor.logs.none') }}</p>
        </div>
        <RouterLink
          v-if="inspectorTarget"
          class="log-detail__inspector"
          data-test="log-inspector-link"
          :to="inspectorTarget"
        >
          {{ t('monitor.logs.drawer.openInspector') }}
        </RouterLink>
      </section>

      <section
        class="log-detail__section"
        data-test="log-usage-cost"
        aria-labelledby="log-detail-usage-heading"
      >
        <h2 id="log-detail-usage-heading">{{ t('monitor.logs.drawer.usage.title') }}</h2>
        <p class="log-detail__section-description">
          {{ t('monitor.logs.drawer.usage.description') }}
        </p>
        <div class="log-detail__usage-status">
          <StatusBadge :tone="usageTone">{{ usageStateLabel(log) }}</StatusBadge>
          <StatusBadge :tone="costTone">{{ costStateLabel(log) }}</StatusBadge>
        </div>
        <dl class="log-detail__facts">
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.finalGroup') }}</dt>
            <dd data-test="log-final-group">
              {{
                log.group_id === null
                  ? t('monitor.logs.drawer.usage.unknown')
                  : t('monitor.logs.drawer.usage.groupId', { id: log.group_id })
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.estimatedCost') }}</dt>
            <dd data-test="log-estimated-cost">{{ estimatedCost(log) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.tokens.uncachedInput') }}</dt>
            <dd>{{ tokenValue(log, log.uncached_input_tokens) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.tokens.cacheRead') }}</dt>
            <dd>{{ tokenValue(log, log.cache_read_tokens) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.tokens.cacheWrite5m') }}</dt>
            <dd>{{ tokenValue(log, log.cache_write_5m_tokens) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.tokens.cacheWrite1h') }}</dt>
            <dd>{{ tokenValue(log, log.cache_write_1h_tokens) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.usage.tokens.output') }}</dt>
            <dd>{{ tokenValue(log, log.output_tokens) }}</dd>
          </div>
        </dl>
        <InlineFeedback
          :tone="
            log.usage_state === 'missing' || log.cost_state === 'unpriced' ? 'warning' : 'info'
          "
        >
          {{ aggregationLabel(log) }}
        </InlineFeedback>
        <RouterLink
          v-if="log.cost_state === 'unpriced'"
          class="log-detail__prices"
          data-test="log-usage-prices-link"
          to="/settings/model-prices"
        >
          {{ t('monitor.logs.drawer.usage.openPrices') }}
        </RouterLink>
      </section>

      <section class="log-detail__section" aria-labelledby="log-detail-attempts-heading">
        <h2 id="log-detail-attempts-heading">{{ t('monitor.logs.drawer.attempts') }}</h2>
        <p v-if="orderedAttempts.length === 0" class="log-detail__empty">
          {{ t('monitor.logs.drawer.noAttempts') }}
        </p>
        <article
          v-for="attempt in orderedAttempts"
          v-else
          :key="attempt.sequence"
          class="log-attempt"
          :data-test="`log-attempt-${attempt.sequence}`"
        >
          <header>
            <h3>{{ t('monitor.logs.drawer.attempt', { sequence: attempt.sequence }) }}</h3>
            <StatusBadge :tone="attempt.committed ? 'success' : 'neutral'">
              {{
                attempt.committed
                  ? t('monitor.logs.drawer.committed')
                  : t('monitor.logs.drawer.notCommitted')
              }}
            </StatusBadge>
          </header>
          <dl class="log-detail__facts">
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
                <code>{{ modelLabel(attempt.upstream_model) }}</code>
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.statusCode') }}</dt>
              <dd>{{ attempt.status_code }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.duration') }}</dt>
              <dd>{{ attempt.duration_ms }} ms</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.failureCategory') }}</dt>
              <dd>{{ failureLabel(attempt.failure_category) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.action') }}</dt>
              <dd>{{ actionLabel(attempt.action) }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.willRetry') }}</dt>
              <dd>{{ attempt.will_retry ? t('monitor.logs.yes') : t('monitor.logs.no') }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.errorCode') }}</dt>
              <dd>
                <code>{{ attempt.error_code || t('monitor.logs.none') }}</code>
              </dd>
            </div>
          </dl>
          <div class="log-detail__summary">
            <h4>{{ t('monitor.logs.drawer.errorSummary') }}</h4>
            <p>{{ attempt.error_summary || t('monitor.logs.none') }}</p>
          </div>
        </article>
      </section>
    </div>
  </AppDrawer>
</template>

<style scoped>
.log-detail {
  display: grid;
  min-width: 0;
  gap: var(--space-6);
}

.log-detail__section {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}

.log-detail__section h2,
.log-detail__section h3,
.log-detail__section h4,
.log-detail__section p {
  margin: 0;
}

.log-detail__section-description {
  color: var(--color-text-muted);
}

.log-detail__section h2 {
  font-size: 1rem;
}

.log-detail__facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.log-detail__facts > div {
  min-width: 0;
}

.log-detail__facts dt {
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.log-detail__facts dd {
  margin: var(--space-1) 0 0;
  overflow-wrap: anywhere;
}

.log-detail__wide {
  grid-column: 1 / -1;
}

.log-detail__copy {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-2);
}

.log-detail code,
.log-detail time {
  font-family: var(--font-mono);
}

.log-detail__summary,
.log-attempt {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: var(--space-3);
}

.log-detail__summary {
  display: grid;
  gap: var(--space-2);
}

.log-detail__summary p {
  color: var(--color-text-muted);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.log-detail__inspector {
  display: inline-flex;
  width: fit-content;
  min-width: 0;
  min-height: 44px;
  align-items: center;
  color: var(--color-action);
  font-weight: 650;
  overflow-wrap: anywhere;
}

.log-detail__usage-status {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.log-detail__prices {
  display: inline-flex;
  width: fit-content;
  min-height: 44px;
  align-items: center;
  color: var(--color-action);
  font-weight: 650;
}

.log-attempt {
  display: grid;
  gap: var(--space-3);
}

.log-attempt > header {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-2);
}

.log-detail__empty {
  color: var(--color-text-muted);
}
</style>
