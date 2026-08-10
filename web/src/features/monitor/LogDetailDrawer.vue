<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { protocolLabelKey } from '@/api/control/protocols'
import { useStableLoading } from '@/app/loading-state'
import {
  requestLogDetailQueryOptions,
  type RequestLogAttemptDto,
  type RequestLogPricingLineDto,
} from '@/app/resources/request-logs'
import AppButton from '@/components/ui/AppButton.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
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
import LogProtocolConversion from './LogProtocolConversion.vue'
import LogRouteIdentity from './LogRouteIdentity.vue'

const props = defineProps<{
  open: boolean
  requestId: string | undefined
  selfScoped?: boolean
  groupNames?: Record<number, string>
  channelNames?: Record<string, string>
}>()
defineEmits<{ 'update:open': [open: boolean] }>()
const client = useApiClient()
const { locale, t } = useI18n()
const query = useQuery(requestLogDetailQueryOptions(client, () => props.requestId))
const initialLoading = useStableLoading(() => props.open && query.isPending.value)
const log = computed(() => query.data.value)
const errorMessageExpanded = ref(false)
const expandedAttemptErrorMessages = ref<Set<number>>(new Set())
const mainErrorMessage = computed(() => {
  const value = log.value
  if (!value) return ''
  if (value.error_summary.trim() !== '') return value.error_summary
  return (
    [...value.attempts].reverse().find((attempt) => attempt.error_summary.trim() !== '')
      ?.error_summary ?? ''
  )
})
const usageDisplayState = computed(() =>
  log.value ? requestLogUsageDisplayState(log.value) : 'not_applicable',
)
const costDisplayState = computed(() =>
  log.value ? requestLogCostDisplayState(log.value) : 'not_applicable',
)
const receipt = computed(
  () =>
    log.value?.attempts.find((attempt) => attempt.committed && attempt.pricing_receipt)
      ?.pricing_receipt ??
    log.value?.attempts.find((attempt) => attempt.pricing_receipt)?.pricing_receipt,
)
const pricingIdentity = computed(() => {
  const value = receipt.value
  if (!value) return '—'
  if (value.schema_version === 3) {
    return `${value.rule.channel_id} · ${value.rule.model_id}`
  }
  return `${t(`monitor.logs.receipt.historicalSchema${value.schema_version}`)} · ${value.rule.model_id}`
})
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

watch(
  () => props.requestId,
  () => {
    errorMessageExpanded.value = false
    expandedAttemptErrorMessages.value = new Set()
  },
)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    errorMessageExpanded.value = false
    expandedAttemptErrorMessages.value = new Set()
  },
)

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

function finalGroupName(): string | null {
  const groupID = log.value?.group_id
  if (groupID === null || groupID === undefined) return null
  return (
    [...(log.value?.attempts ?? [])].reverse().find(({ group_id }) => group_id === groupID)
      ?.group_name ??
    props.groupNames?.[groupID] ??
    null
  )
}

function finalChannelName(): string | null {
  const channelID = log.value?.channel_id
  if (!channelID) return null
  return props.channelNames?.[channelID] ?? null
}

function errorMessageNeedsDisclosure(message: string): boolean {
  return message.length > 240
}

function normalizedErrorMessage(message: string): string {
  return message.replace(/\s+/g, ' ').trim()
}

function attemptErrorMessage(attempt: RequestLogAttemptDto): string {
  const message = attempt.error_summary
  if (message.trim() === '') return ''
  if (normalizedErrorMessage(message) === normalizedErrorMessage(mainErrorMessage.value)) return ''
  const firstMatchingAttempt = log.value?.attempts.find(
    (candidate) =>
      normalizedErrorMessage(candidate.error_summary) === normalizedErrorMessage(message),
  )
  if (firstMatchingAttempt && firstMatchingAttempt.sequence !== attempt.sequence) return ''
  return message
}

function isAttemptErrorMessageExpanded(sequence: number): boolean {
  return expandedAttemptErrorMessages.value.has(sequence)
}

function toggleAttemptErrorMessage(sequence: number): void {
  const next = new Set(expandedAttemptErrorMessages.value)
  if (next.has(sequence)) next.delete(sequence)
  else next.add(sequence)
  expandedAttemptErrorMessages.value = next
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
          <OverflowTooltip as="code" :content="log.request_id">
            {{ log.request_id }}
          </OverflowTooltip>
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
          <div v-if="!selfScoped">
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
        </dl>
        <div v-if="mainErrorMessage" class="log-error-message">
          <p class="log-error-message__label">{{ t('monitor.logs.drawer.errorSummary') }}</p>
          <p
            class="log-error-message__content"
            :class="{
              'log-error-message__content--collapsed':
                !errorMessageExpanded && errorMessageNeedsDisclosure(mainErrorMessage),
            }"
          >
            {{ mainErrorMessage }}
          </p>
          <AppButton
            v-if="errorMessageNeedsDisclosure(mainErrorMessage)"
            class="log-error-message__toggle"
            variant="link"
            size="inline"
            :aria-expanded="errorMessageExpanded"
            @click="errorMessageExpanded = !errorMessageExpanded"
          >
            {{
              errorMessageExpanded
                ? t('monitor.logs.drawer.collapseErrorMessage')
                : t('monitor.logs.drawer.expandErrorMessage')
            }}
          </AppButton>
        </div>
      </section>

      <section class="log-detail__section">
        <h3>{{ t('monitor.logs.drawer.route') }}</h3>
        <dl class="log-detail__grid">
          <div v-if="!selfScoped">
            <dt>{{ t('monitor.logs.drawer.accessKey') }}</dt>
            <dd>{{ accessKeyLabel() }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.protocol') }}</dt>
            <dd class="log-detail__protocol">
              <span>{{ t(protocolLabelKey(log.protocol)) }}</span>
              <LogProtocolConversion
                :mode="log.route_mode"
                :client-protocol="log.protocol"
                :upstream-protocol="finalChannelName() ?? log.channel_id"
              />
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.clientModel') }}</dt>
            <dd>
              <code>{{ log.client_model ?? '—' }}</code>
            </dd>
          </div>
          <div v-if="!selfScoped">
            <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
            <dd>
              <code>{{ log.upstream_model ?? '—' }}</code>
            </dd>
          </div>
          <div v-if="!selfScoped" class="log-detail__wide">
            <dt>{{ t('monitor.logs.drawer.routeIdentity') }}</dt>
            <dd>
              <LogRouteIdentity
                :group-id="log.group_id"
                :group-name="finalGroupName()"
                :channel-id="log.channel_id"
                :channel-name="finalChannelName()"
                :credential-id="log.credential_id"
              />
            </dd>
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
            v-if="
              !selfScoped &&
              costDisplayState !== 'unpriced' &&
              receipt &&
              usageDisplayState === 'reported'
            "
            class="log-detail__wide"
          >
            <dt>{{ t('monitor.logs.receipt.formula') }}</dt>
            <dd class="log-detail__formula">
              <span>{{ t('monitor.logs.receipt.input') }} = {{ formula.input }}</span>
              <span>{{ t('monitor.logs.receipt.output') }} = {{ formula.output }}</span>
            </dd>
          </div>
          <div v-if="!selfScoped && receipt">
            <dt>{{ t('monitor.logs.receipt.schema') }}</dt>
            <dd>v{{ receipt.schema_version }}</dd>
          </div>
          <div v-if="!selfScoped && receipt">
            <dt>{{ t('monitor.logs.receipt.identity') }}</dt>
            <dd>
              <code>{{ pricingIdentity }}</code>
            </dd>
          </div>
        </dl>
      </section>

      <section v-if="!selfScoped" class="log-detail__section">
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
            <div class="log-detail__wide">
              <dt>{{ t('monitor.logs.drawer.routeIdentity') }}</dt>
              <dd class="log-detail__route-identity">
                <LogRouteIdentity
                  :group-id="attempt.group_id"
                  :group-name="attempt.group_name"
                  :channel-id="attempt.channel_id"
                  :channel-name="
                    attempt.channel_id ? (channelNames?.[attempt.channel_id] ?? null) : null
                  "
                  :credential-id="attempt.credential_id"
                />
                <LogProtocolConversion
                  :mode="attempt.route_mode"
                  :client-protocol="log.protocol"
                  :upstream-protocol="
                    attempt.channel_id
                      ? (channelNames?.[attempt.channel_id] ?? attempt.channel_id)
                      : null
                  "
                />
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
              <dd>
                <code>{{ attempt.upstream_model ?? '—' }}</code>
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.operation') }}</dt>
              <dd>
                <code>{{ attempt.operation ?? '—' }}</code>
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.dispatchState') }}</dt>
              <dd>
                <code>{{ attempt.dispatch_state ?? '—' }}</code>
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.responseStarted') }}</dt>
              <dd>{{ attempt.response_started ? t('monitor.logs.yes') : t('monitor.logs.no') }}</dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.committed') }}</dt>
              <dd>
                {{
                  attempt.committed
                    ? t('monitor.logs.drawer.committed')
                    : t('monitor.logs.drawer.notCommitted')
                }}
              </dd>
            </div>
            <div v-if="attempt.upstream_request_id">
              <dt>{{ t('monitor.logs.drawer.upstreamRequestId') }}</dt>
              <dd>
                <code>{{ attempt.upstream_request_id }}</code>
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
          </dl>
          <div
            v-if="attemptErrorMessage(attempt)"
            class="log-error-message log-error-message--attempt"
          >
            <p class="log-error-message__label">{{ t('monitor.logs.drawer.errorSummary') }}</p>
            <p
              class="log-error-message__content"
              :class="{
                'log-error-message__content--collapsed':
                  !isAttemptErrorMessageExpanded(attempt.sequence) &&
                  errorMessageNeedsDisclosure(attemptErrorMessage(attempt)),
              }"
            >
              {{ attemptErrorMessage(attempt) }}
            </p>
            <AppButton
              v-if="errorMessageNeedsDisclosure(attemptErrorMessage(attempt))"
              class="log-error-message__toggle"
              variant="link"
              size="inline"
              :aria-expanded="isAttemptErrorMessageExpanded(attempt.sequence)"
              @click="toggleAttemptErrorMessage(attempt.sequence)"
            >
              {{
                isAttemptErrorMessageExpanded(attempt.sequence)
                  ? t('monitor.logs.drawer.collapseErrorMessage')
                  : t('monitor.logs.drawer.expandErrorMessage')
              }}
            </AppButton>
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

.log-detail__protocol {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
}

.log-detail__route-identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
}

.log-detail__route-identity > :first-child {
  min-width: 0;
}

.log-detail__wide {
  grid-column: 1 / -1;
}

.log-error-message {
  display: grid;
  gap: 4px;
  margin-top: 16px;
  border-left: 2px solid var(--color-danger);
  background: var(--color-danger-bg);
  padding: 10px 12px;
}

.log-error-message__label {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.log-error-message__content {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  line-height: 1.6;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.log-error-message__content--collapsed {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
}

.log-error-message__toggle {
  justify-self: start;
  margin-top: 2px;
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

.log-error-message--attempt {
  margin-top: 12px;
  border-left-color: var(--color-border-control);
  background: transparent;
  padding: 10px 0 0 12px;
}

.log-error-message--attempt .log-error-message__content--collapsed {
  -webkit-line-clamp: 1;
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
