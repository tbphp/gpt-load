<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import type { ChannelDto } from '@/app/resources/channels'
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
  formatLogTokenCount,
  formatRequestLogReasoning,
  requestLogCostDisplayState,
  requestLogUsageDisplayState,
} from './log-format'
import LogRouteIdentity from './LogRouteIdentity.vue'
import PricingModeIndicator from './PricingModeIndicator.vue'

const props = defineProps<{
  open: boolean
  requestId: string | undefined
  selfScoped?: boolean
  groupNames?: Record<number, string>
  channels?: Record<string, ChannelDto>
}>()
defineEmits<{ 'update:open': [open: boolean] }>()
const client = useApiClient()
const { locale, t } = useI18n()
const query = useQuery(requestLogDetailQueryOptions(client, () => props.requestId))
const initialLoading = useStableLoading(() => props.open && query.isPending.value)
const log = computed(() => query.data.value)
const errorMessageExpanded = ref(false)
const expandedAttemptErrorMessages = ref<Set<number>>(new Set())
const finalAttempt = computed(() => {
  const value = log.value
  if (!value || value.attempts.length === 0) return null
  const attempts = [...value.attempts].reverse()
  return (
    attempts.find(
      (attempt) =>
        attempt.group_id === value.group_id &&
        attempt.channel_id === value.channel_id &&
        attempt.credential_id === value.credential_id,
    ) ?? attempts[0]
  )
})
const mainErrorMessage = computed(() => log.value?.error_summary ?? '')
const mainErrorCode = computed(() => log.value?.error_code ?? '')
const drawerDescription = computed(() =>
  t(
    props.selfScoped
      ? 'monitor.logs.drawer.descriptionSelfScoped'
      : 'monitor.logs.drawer.description',
  ),
)
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
  if (value.schema_version >= 3) {
    return `${channelName(value.rule.channel_id)} · ${value.rule.model_id}`
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
  return t(`monitor.logs.drawer.usage.costState.${state}`)
})

const costAmountLabel = computed(() => {
  if (!log.value) return '—'
  const state = costDisplayState.value
  if (state === 'complete') {
    return formatEstimatedCost(log.value.estimated_cost_nano_usd, locale.value)
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

function operationLabel(operation: RequestLogAttemptDto['operation']): string {
  if (operation === null) return t('monitor.logs.protocolConversion.notRecorded')
  return t(`monitor.logs.operation.${operation}`)
}

function upstreamProtocolLabel(
  upstreamProtocol: RequestLogAttemptDto['upstream_protocol'],
): string {
  if (upstreamProtocol === null) return t('monitor.logs.protocolConversion.notRecorded')
  return upstreamProtocol
}

function reasoningLabel(reasoning: RequestLogAttemptDto['reasoning']): string {
  const value = formatRequestLogReasoning(reasoning, locale.value)
  return value ? `[${value}]` : ''
}

function upstreamReasoningLabel(
  reasoning: RequestLogAttemptDto['reasoning'],
  routeMode: RequestLogAttemptDto['route_mode'],
): string {
  return reasoningLabel(routeMode === 'converted' ? reasoning : (log.value?.reasoning ?? null))
}

function isFinalAttempt(attempt: RequestLogAttemptDto): boolean {
  return finalAttempt.value?.sequence === attempt.sequence
}

function showAttemptOperation(attempt: RequestLogAttemptDto): boolean {
  const requestOperation = log.value?.operation ?? null
  return (
    attempt.operation !== null &&
    (requestOperation === null || attempt.operation !== requestOperation)
  )
}

function dispatchStateLabel(attempt: RequestLogAttemptDto): string {
  if (attempt.response_started) return t('monitor.logs.dispatchState.response_started')
  if (attempt.dispatch_state === null) return t('monitor.logs.protocolConversion.notRecorded')
  return t(`monitor.logs.dispatchState.${attempt.dispatch_state}`)
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

function channelDefinition(channelID: string | null | undefined): ChannelDto | null {
  if (!channelID) return null
  return props.channels?.[channelID] ?? null
}

function channelName(channelID: string | null | undefined): string {
  if (!channelID) return '—'
  return channelDefinition(channelID)?.name.trim() || channelID
}

function finalChannel(): ChannelDto | null {
  return channelDefinition(log.value?.channel_id)
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
    :description="drawerDescription"
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
        <div v-if="mainErrorCode || mainErrorMessage" class="log-error-message">
          <p v-if="mainErrorCode" class="log-error-message__code">
            <span>{{ t('monitor.logs.drawer.errorCode') }}</span>
            <code>{{ mainErrorCode }}</code>
          </p>
          <p v-if="mainErrorMessage" class="log-error-message__label">
            {{ t('monitor.logs.drawer.errorSummary') }}
          </p>
          <p
            v-if="mainErrorMessage"
            class="log-error-message__content"
            :class="{
              'log-error-message__content--collapsed':
                !errorMessageExpanded && errorMessageNeedsDisclosure(mainErrorMessage),
            }"
          >
            {{ mainErrorMessage }}
          </p>
          <AppButton
            v-if="mainErrorMessage && errorMessageNeedsDisclosure(mainErrorMessage)"
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
        <h3>{{ t('monitor.logs.drawer.request') }}</h3>
        <dl class="log-detail__grid">
          <div v-if="!selfScoped">
            <dt>{{ t('monitor.logs.drawer.accessKey') }}</dt>
            <dd>{{ accessKeyLabel() }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.protocol') }}</dt>
            <dd>
              <code>{{ log.protocol }}</code>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.operation') }}</dt>
            <dd>{{ operationLabel(log.operation) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.clientModel') }}</dt>
            <dd class="log-detail__model-value">
              <code>{{ log.client_model ?? '—' }}</code
              ><small v-if="reasoningLabel(log.reasoning)" class="log-detail__reasoning">{{
                reasoningLabel(log.reasoning)
              }}</small>
            </dd>
          </div>
        </dl>
      </section>

      <section v-if="!selfScoped" class="log-detail__section">
        <h3>{{ t('monitor.logs.drawer.finalExecution') }}</h3>
        <dl class="log-detail__grid">
          <div class="log-detail__wide">
            <dt>{{ t('monitor.logs.drawer.routeIdentity') }}</dt>
            <dd>
              <LogRouteIdentity
                :group-id="log.group_id"
                :group-name="finalGroupName()"
                :channel-id="log.channel_id"
                :channel="finalChannel()"
                :credential-id="log.credential_id"
                appearance="plain"
              />
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.upstreamProtocol') }}</dt>
            <dd>{{ upstreamProtocolLabel(log.upstream_protocol) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
            <dd class="log-detail__model-value">
              <code>{{ log.upstream_model ?? '—' }}</code
              ><small
                v-if="
                  upstreamReasoningLabel(
                    finalAttempt?.reasoning ?? null,
                    finalAttempt?.route_mode ?? null,
                  )
                "
                class="log-detail__reasoning"
                >{{
                  upstreamReasoningLabel(
                    finalAttempt?.reasoning ?? null,
                    finalAttempt?.route_mode ?? null,
                  )
                }}</small
              >
            </dd>
          </div>
        </dl>
        <div
          v-if="log.model_consistency === 'unknown' || log.model_consistency === 'mismatch'"
          class="log-model-observation"
          :class="`log-model-observation--${log.model_consistency}`"
        >
          <div class="log-model-observation__heading">
            <strong>{{ t('monitor.logs.drawer.modelObservation') }}</strong>
            <StatusBadge
              :tone="log.model_consistency === 'mismatch' ? 'warning' : 'neutral'"
              size="compact"
            >
              {{
                t(
                  log.model_consistency === 'mismatch'
                    ? 'monitor.logs.modelConsistency.mismatchLabel'
                    : 'monitor.logs.modelConsistency.unknownLabel',
                )
              }}
            </StatusBadge>
          </div>
          <dl class="log-detail__grid">
            <div>
              <dt>{{ t('monitor.logs.drawer.requestedModel') }}</dt>
              <dd>
                <code>{{ log.upstream_model ?? '—' }}</code>
              </dd>
            </div>
            <div>
              <dt>{{ t('monitor.logs.drawer.reportedModel') }}</dt>
              <dd>
                <code>{{
                  log.upstream_reported_model ?? t('monitor.logs.modelConsistency.notObserved')
                }}</code>
              </dd>
            </div>
          </dl>
        </div>
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
            <dd class="log-detail__cost">
              <span>{{ costAmountLabel }}</span>
              <PricingModeIndicator
                :mode="log.pricing_mode"
                :context-threshold-tokens="log.context_threshold_tokens"
              />
            </dd>
          </div>
          <div v-if="!selfScoped && receipt">
            <dt>{{ t('monitor.logs.receipt.identity') }}</dt>
            <dd>
              <code>{{ pricingIdentity }}</code>
            </dd>
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
        </dl>
      </section>

      <section v-if="!selfScoped" class="log-detail__section log-detail__attempt-section">
        <details v-if="log.attempts.length > 0" class="log-attempt-chain">
          <summary>
            <ChevronRight class="log-attempt-chain__chevron" :size="15" aria-hidden="true" />
            <span>{{ t('monitor.logs.drawer.attempts') }}</span>
            <span class="log-attempt-chain__count">{{ log.attempts.length }}</span>
          </summary>
          <article v-for="attempt in log.attempts" :key="attempt.sequence" class="log-attempt">
            <header>
              <span>{{ t('monitor.logs.drawer.attempt', { sequence: attempt.sequence }) }}</span>
              <StatusBadge :tone="attemptTone(attempt)" size="compact">
                {{ t(`monitor.logs.failureCategory.${attempt.failure_category}`)
                }}<template v-if="attempt.status_code"> · {{ attempt.status_code }}</template>
              </StatusBadge>
            </header>
            <dl class="log-detail__grid">
              <div v-if="!isFinalAttempt(attempt)" class="log-detail__wide">
                <dt>{{ t('monitor.logs.drawer.routeIdentity') }}</dt>
                <dd>
                  <LogRouteIdentity
                    :group-id="attempt.group_id"
                    :group-name="attempt.group_name"
                    :channel-id="attempt.channel_id"
                    :channel="channelDefinition(attempt.channel_id)"
                    :credential-id="attempt.credential_id"
                    appearance="plain"
                  />
                </dd>
              </div>
              <template v-if="!isFinalAttempt(attempt)">
                <div>
                  <dt>{{ t('monitor.logs.drawer.upstreamProtocol') }}</dt>
                  <dd>{{ upstreamProtocolLabel(attempt.upstream_protocol) }}</dd>
                </div>
                <div>
                  <dt>{{ t('monitor.logs.drawer.upstreamModel') }}</dt>
                  <dd class="log-detail__model-value">
                    <code>{{ attempt.upstream_model ?? '—' }}</code
                    ><small
                      v-if="upstreamReasoningLabel(attempt.reasoning, attempt.route_mode)"
                      class="log-detail__reasoning"
                      >{{ upstreamReasoningLabel(attempt.reasoning, attempt.route_mode) }}</small
                    >
                  </dd>
                </div>
              </template>
              <div v-if="showAttemptOperation(attempt)">
                <dt>{{ t('monitor.logs.drawer.operation') }}</dt>
                <dd>{{ operationLabel(attempt.operation) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.logs.drawer.dispatchState') }}</dt>
                <dd>{{ dispatchStateLabel(attempt) }}</dd>
              </div>
              <div v-if="log.stream">
                <dt>{{ t('monitor.logs.drawer.clientStreamState') }}</dt>
                <dd>
                  {{
                    attempt.committed
                      ? t('monitor.logs.drawer.clientStreamStarted')
                      : t('monitor.logs.drawer.clientStreamNotStarted')
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
              <div v-if="attempt.failure_origin">
                <dt>{{ t('monitor.logs.drawer.failureOrigin') }}</dt>
                <dd>{{ t(`monitor.logs.failureOrigin.${attempt.failure_origin}`) }}</dd>
              </div>
              <div v-if="attempt.failure_scope">
                <dt>{{ t('monitor.logs.drawer.failureScope') }}</dt>
                <dd>{{ t(`monitor.logs.failureScope.${attempt.failure_scope}`) }}</dd>
              </div>
              <div v-if="attempt.retry_directive">
                <dt>{{ t('monitor.logs.drawer.retryDirective') }}</dt>
                <dd>{{ t(`monitor.logs.retryDirective.${attempt.retry_directive}`) }}</dd>
              </div>
              <div v-if="attempt.effect">
                <dt>{{ t('monitor.logs.drawer.effect') }}</dt>
                <dd>{{ t(`monitor.logs.effect.${attempt.effect}`) }}</dd>
              </div>
              <div v-if="attempt.rule_id" class="log-detail__wide">
                <dt>{{ t('monitor.logs.drawer.ruleId') }}</dt>
                <dd>
                  <code>{{ attempt.rule_id }}</code>
                </dd>
              </div>
              <div v-if="!attempt.retry_directive && !attempt.effect">
                <dt>{{ t('monitor.logs.drawer.action') }}</dt>
                <dd>{{ t(`monitor.logs.action.${attempt.action}`) }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.logs.drawer.subsequentAttempt') }}</dt>
                <dd>
                  {{
                    attempt.will_retry
                      ? t('monitor.logs.drawer.subsequentAttemptOccurred')
                      : t('monitor.logs.drawer.noSubsequentAttempt')
                  }}
                </dd>
              </div>
            </dl>
            <div
              v-if="attempt.error_code || attemptErrorMessage(attempt)"
              class="log-error-message log-error-message--attempt"
            >
              <p v-if="attempt.error_code" class="log-error-message__code">
                <span>{{ t('monitor.logs.drawer.errorCode') }}</span>
                <code>{{ attempt.error_code }}</code>
              </p>
              <p v-if="attemptErrorMessage(attempt)" class="log-error-message__label">
                {{ t('monitor.logs.drawer.errorSummary') }}
              </p>
              <p
                v-if="attemptErrorMessage(attempt)"
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
                v-if="
                  attemptErrorMessage(attempt) &&
                  errorMessageNeedsDisclosure(attemptErrorMessage(attempt))
                "
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
        </details>
        <p v-else class="log-detail__empty">
          {{ t('monitor.logs.drawer.noAttempts') }}
        </p>
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

.log-detail__attempt-section {
  padding: 8px 0;
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

.log-detail__reasoning {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 400;
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

.log-error-message__code {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 8px;
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.log-error-message__code code {
  color: var(--color-danger);
  overflow-wrap: anywhere;
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

.log-detail__cost {
  display: flex;
  align-items: center;
  gap: 6px;
}

.log-model-observation {
  display: grid;
  gap: 10px;
  margin-top: 12px;
  border-left: 2px solid var(--color-border-control);
  background: var(--color-surface-sunken);
  padding: 10px 12px;
}

.log-model-observation--mismatch {
  border-left-color: var(--color-warning);
}

.log-model-observation__heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: var(--text-label-xs);
}

.log-attempt-chain {
  margin: 0;
}

.log-attempt-chain summary {
  display: flex;
  min-height: 24px;
  align-items: center;
  gap: 6px;
  color: var(--color-text);
  cursor: pointer;
  font-size: var(--text-sm);
  font-weight: 650;
  list-style: none;
}

.log-attempt-chain summary::-webkit-details-marker {
  display: none;
}

.log-attempt-chain__chevron {
  color: var(--color-text-faint);
  transition: transform 140ms ease;
}

.log-attempt-chain[open] .log-attempt-chain__chevron {
  transform: rotate(90deg);
}

.log-attempt-chain__count {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-weight: 400;
}

.log-attempt-chain[open] .log-attempt:first-of-type {
  margin-top: 4px;
}

.log-attempt + .log-attempt {
  border-top: 1px solid var(--color-border-subtle);
}

.log-attempt {
  padding: 8px 0;
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
  margin-bottom: 8px;
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
