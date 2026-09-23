<script setup lang="ts">
import { Activity, ArrowDownToLine, Clock3, Coins, ShieldCheck } from '@lucide/vue'
import { timeRangeQuery } from '@modern/app/time-range'
import type { DateRangePreset } from '@modern/components/ui/date-time'
import { useQuery } from '@tanstack/vue-query'
import { DialogRoot } from 'reka-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { getLogDetail, logDetailKey, type LogReasoning } from '@modern/api/logs'
import type { GroupRow } from '@modern/api/groups'
import type { GroupChannel } from '@modern/api/group-create'
import { useMessages, useMessageSource } from '@modern/app/messages'
import {
  AppBadge,
  AppProtocolTag,
  AppButton,
  AppCollectionState,
  AppCopyValue,
  AppDialogContent,
  AppDialogHeader,
  AppFormSection,
  AppOverflowText,
  AppIcon,
  AppChannelIcon,
  AppTooltip,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import type { LogColumnId } from './log-columns'
import {
  exactLogMoney,
  logCacheWrites,
  logDuration,
  logNumber,
  logOutputRate,
  logStatusTone,
  logTime,
} from './log-display'
import { createRedactedLogExport } from './log-redacted-export'
import LogPricingReceipt from './LogPricingReceipt.vue'
import LogValue from './LogValue.vue'
import LogModelWarning from './LogModelWarning.vue'
import LogCredentialValue from './LogCredentialValue.vue'

const props = defineProps<{
  id: string
  admin: boolean
  groups?: ReadonlyMap<number, GroupRow>
  channels?: ReadonlyMap<string, GroupChannel>
  from: string
  to: string
  preset?: DateRangePreset
}>()
defineEmits<{ close: [] }>()
const { t, te, n, locale } = useI18n()
const client = useApiClient()
const messages = useMessages()
const query = useQuery({
  queryKey: [...logDetailKey(props.id), props.admin],
  queryFn: ({ signal }) => getLogDetail(client, props.id, signal),
})
const log = computed(() => query.data.value)
const outputRate = computed(() => (log.value ? logOutputRate(log.value, locale.value) : '—'))
const outcomeFields = computed<LogColumnId[]>(() => [
  'status_code',
  'stream',
  'operation',
  ...(props.admin ? ['attempt_count' as const, 'affinity_hit' as const] : []),
])
const routingFields: LogColumnId[] = [
  'access_key',
  'group',
  'channel',
  'credential_name',
  'upstream_model',
  'upstream_reported_model',
  'model_consistency',
  'upstream_protocol',
  'route_mode',
]
const tokenFields = computed<LogColumnId[]>(() => [
  'input_tokens',
  'output_tokens',
  'total_tokens',
  'cache_read_tokens',
  'cache_hit_rate',
  'cache_write_tokens',
  // 细分写入绝大多数请求都是 0，只在确实写过缓存时才展开。
  ...(log.value && logCacheWrites(log.value) !== '0'
    ? (['cache_write_5m_tokens', 'cache_write_1h_tokens', 'cache_write_unknown_tokens'] as const)
    : []),
  'usage_state',
])
const costFields = computed<LogColumnId[]>(() => [
  'estimated_cost_nano_usd',
  'cost_state',
  'pricing_completeness',
  'pricing_mode',
  'context_threshold_tokens',
])
const primaryFields = [
  { field: 'duration_ms', icon: Clock3 },
  { field: 'first_response_ms', icon: Activity },
  { field: 'input_tokens', icon: ArrowDownToLine },
  { field: 'estimated_cost_nano_usd', icon: Coins },
] as const
const receipt = computed(
  () =>
    log.value?.attempts.find((attempt) => attempt.committed && attempt.pricing_receipt)
      ?.pricing_receipt ??
    log.value?.attempts.find((attempt) => attempt.pricing_receipt)?.pricing_receipt,
)
function valueName(value: string | null | undefined): string {
  return !value ? '—' : te('logs.values.' + value) ? t('logs.values.' + value) : value
}
function decisionStrategy(source: string): string {
  const key = 'autoModel.sources.' + source
  return te(key) ? t(key) : `${t('autoModel.sources.unknown')} · ${source}`
}
function decisionPhase(phase: string): string {
  const key = 'autoModel.phases.' + phase
  return te(key) ? t(key) : `${t('autoModel.phases.unknown')} · ${phase}`
}
function decisionReason(reason: string): string {
  if (!reason) return ''
  if (/^http_\d+$/u.test(reason))
    return t('autoModel.reasons.httpError', { status: reason.slice('http_'.length) })
  const key = 'autoModel.reasons.' + reason
  return te(key) ? t(key) : `${t('autoModel.reasons.unknown')} · ${reason}`
}
function confidenceText(value: number): string {
  return n(value, { style: 'percent', maximumFractionDigits: 1 })
}
function decisionModelText(requested: string, upstream: string, reported: string): string {
  const selected =
    requested && upstream && requested !== upstream
      ? `${requested} → ${upstream}`
      : upstream || requested
  const observed =
    reported && reported !== upstream ? `${t('autoModel.reportedModel')} ${reported}` : ''
  return [selected, observed].filter(Boolean).join(' · ') || '—'
}
function decisionRouteText(
  group: string,
  channel: string,
  credential: string,
  credentialDeleted: boolean,
): string {
  return (
    [group, channel, credential || (credentialDeleted ? t('autoModel.deletedCredential') : '')]
      .filter(Boolean)
      .join(' · ') || '—'
  )
}
// 表格按 强度 > 预算 > 开关 只取一个值，详情面板给出完整拆解。
function reasoningText(value: LogReasoning): string {
  const budget =
    value.budget_tokens && value.budget_tokens !== '0'
      ? value.budget_tokens === '-1'
        ? t('logs.values.auto')
        : logNumber(value.budget_tokens, locale.value)
      : ''
  return [value.mode && valueName(value.mode), value.effort, budget].filter(Boolean).join(' · ')
}
const usageLocation = computed(() => ({
  name: 'modern-usage',
  query: {
    ...(props.admin && log.value?.access_key.id
      ? { access_key_id: String(log.value.access_key.id) }
      : {}),
    ...timeRangeQuery({ preset: props.preset, from_ms: props.from, to_ms: props.to }),
  },
}))
useMessageSource(() =>
  query.isError.value && log.value
    ? {
        tone: 'warning',
        text: t('logs.stale'),
        action: { label: t('ui.retry'), run: () => query.refetch() },
      }
    : undefined,
)
function resolveRedactedLog(): Promise<string> {
  if (!log.value) throw new Error('LOG_NOT_AVAILABLE')
  return createRedactedLogExport(log.value)
}
</script>

<template>
  <DialogRoot
    :open="true"
    @update:open="
      (open) => {
        if (!open) $emit('close')
      }
    "
  >
    <AppDialogContent
      placement="editor"
      :title="t('logs.details')"
      :description="t('logs.detailDescription')"
    >
      <AppDialogHeader
        :title="t('logs.details')"
        :close-label="t('ui.close')"
        @close="$emit('close')"
      />
      <div class="modern-log-detail-body">
        <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
        <AppCollectionState
          v-else-if="!log"
          :title="t(query.isError.value ? 'logs.loadFailed' : 'logs.notFound')"
          :error="query.isError.value"
          ><AppButton v-if="query.isError.value" @click="query.refetch()">{{
            t('ui.retry')
          }}</AppButton></AppCollectionState
        >
        <template v-else>
          <section
            class="modern-log-result"
            :class="'modern-log-result--' + logStatusTone[log.status]"
          >
            <header class="modern-log-result-heading">
              <AppOverflowText class="modern-log-result-model" :text="log.client_model || '—'" />
              <AppBadge :tone="logStatusTone[log.status]" dot>{{
                t('logs.values.' + log.status)
              }}</AppBadge>
            </header>
            <div class="modern-log-result-context">
              <AppProtocolTag :protocol="log.protocol" />
              <time>{{ logTime(log.completed_at_ms, locale, true) }}</time>
            </div>
            <div class="modern-log-request-identity">
              <span>{{ t('logs.columns.request_id') }}</span
              ><AppCopyValue :value="log.request_id" :label="t('logs.copyRequest')" />
            </div>
            <LogModelWarning v-if="admin" :row="log" detail />
            <div
              v-if="log.error_code || log.error_summary"
              class="modern-log-error"
              :class="{ 'is-note': log.status === 'success' }"
            >
              <AppCopyValue
                v-if="log.error_code"
                :value="log.error_code"
                :label="t('logs.copyError')"
              />
              <p v-if="log.error_summary">{{ log.error_summary }}</p>
            </div>
            <dl class="modern-log-primary-metrics">
              <div v-for="item in primaryFields" :key="item.field">
                <dt>
                  <AppIcon :icon="item.icon" size="sm" />{{ t('logs.columns.' + item.field) }}
                </dt>
                <dd>
                  <LogValue :row="log" :column="item.field" :groups="groups" :channels="channels" />
                </dd>
              </div>
            </dl>
            <dl class="modern-log-result-meta">
              <div v-for="field in outcomeFields" :key="field">
                <dt>{{ t('logs.columns.' + field) }}</dt>
                <dd>
                  <template v-if="field === 'stream'">
                    {{ t(log.stream ? 'logs.yes' : 'logs.no') }}
                    <AppTooltip v-if="outputRate !== '—'" :label="t('logs.outputRate')">
                      <span
                        class="modern-log-stream-rate"
                        tabindex="0"
                        :aria-label="t('logs.outputRate') + ': ' + outputRate"
                        >&nbsp;·&nbsp;{{ outputRate }}</span
                      >
                    </AppTooltip>
                  </template>
                  <LogValue
                    v-else
                    :row="log"
                    :column="field"
                    :groups="groups"
                    :channels="channels"
                  />
                </dd>
              </div>
              <div v-if="log.reasoning">
                <dt>{{ t('logs.reasoning') }}</dt>
                <dd>{{ reasoningText(log.reasoning) || '—' }}</dd>
              </div>
            </dl>
          </section>
          <AppFormSection
            v-if="log.request_audit && log.request_audit.status !== 'passed'"
            :title="t('requestAudit.title')"
            compact
          >
            <dl class="modern-log-detail-grid">
              <div>
                <dt>{{ t('requestAudit.result') }}</dt>
                <dd>{{ t('requestAudit.statuses.' + log.request_audit.status) }}</dd>
              </div>
              <div v-if="log.request_audit.reason">
                <dt>{{ t('autoModel.reason') }}</dt>
                <dd>{{ t('requestAudit.reasons.' + log.request_audit.reason) }}</dd>
              </div>
              <div v-for="finding in log.request_audit.findings" :key="finding.rule_id">
                <dt>{{ finding.name }}</dt>
                <dd>{{ t('requestAudit.actions.' + finding.action) }}</dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection v-if="log.auto_decision" :title="t('autoModel.log')" compact>
            <dl class="modern-log-detail-grid">
              <div>
                <dt>{{ t('autoModel.source') }}</dt>
                <dd>{{ decisionStrategy(log.auto_decision.source) }}</dd>
              </div>
              <div v-if="log.auto_decision.execution_phase">
                <dt>{{ t('autoModel.phase') }}</dt>
                <dd>{{ decisionPhase(log.auto_decision.execution_phase) }}</dd>
              </div>
              <div>
                <dt>{{ t('autoModel.selected') }}</dt>
                <dd>{{ log.auto_decision.selection.preset_name }}</dd>
              </div>
              <div>
                <dt>{{ t('autoModel.targetModelLog') }}</dt>
                <dd>{{ log.auto_decision.selection.target_model }}</dd>
              </div>
              <div
                v-if="
                  log.auto_decision.upstream_model ||
                  log.auto_decision.reported_model ||
                  log.auto_decision.requested_model
                "
              >
                <dt>{{ t('autoModel.decisionModel') }}</dt>
                <dd>
                  {{
                    decisionModelText(
                      log.auto_decision.requested_model,
                      log.auto_decision.upstream_model,
                      log.auto_decision.reported_model,
                    )
                  }}
                </dd>
              </div>
              <div
                v-if="
                  log.auto_decision.group_name ||
                  log.auto_decision.channel_name ||
                  log.auto_decision.credential_name ||
                  log.auto_decision.credential_deleted
                "
              >
                <dt>{{ t('autoModel.decisionRoute') }}</dt>
                <dd>
                  {{
                    decisionRouteText(
                      log.auto_decision.group_name,
                      log.auto_decision.channel_name,
                      log.auto_decision.credential_name,
                      log.auto_decision.credential_deleted,
                    )
                  }}
                </dd>
              </div>
              <div v-if="log.auto_decision.called">
                <dt>{{ t('autoModel.duration') }}</dt>
                <dd>{{ log.auto_decision.duration_ms }} ms</dd>
              </div>
              <div v-if="log.auto_decision.confidence !== null">
                <dt>{{ t('autoModel.confidenceValue') }}</dt>
                <dd>{{ confidenceText(log.auto_decision.confidence) }}</dd>
              </div>
              <div v-if="log.auto_decision.reason">
                <dt>{{ t('autoModel.reason') }}</dt>
                <dd>{{ decisionReason(log.auto_decision.reason) }}</dd>
              </div>
              <div>
                <dt>{{ t('autoModel.decisionCost') }}</dt>
                <dd>
                  {{ exactLogMoney(log.auto_decision.estimated_cost_nano_usd) }}
                </dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection v-if="admin" :title="t('logs.routingInfo')" compact>
            <dl class="modern-log-detail-grid">
              <div v-for="field in routingFields" :key="field">
                <dt>{{ t('logs.columns.' + field) }}</dt>
                <dd>
                  <LogValue :row="log" :column="field" :groups="groups" :channels="channels" />
                </dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection :title="t('logs.usageInfo')" compact>
            <dl class="modern-log-detail-grid is-numeric">
              <div v-for="field in tokenFields" :key="field">
                <dt>{{ t('logs.columns.' + field) }}</dt>
                <dd>
                  <LogValue :row="log" :column="field" :groups="groups" :channels="channels" />
                </dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection :title="t('logs.costInfo')" compact>
            <dl class="modern-log-detail-grid is-numeric">
              <div v-for="field in costFields" :key="field">
                <dt>{{ t('logs.columns.' + field) }}</dt>
                <dd>
                  <LogValue :row="log" :column="field" :groups="groups" :channels="channels" />
                </dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection
            v-if="
              receipt || log.auto_decision || log.request_audit?.calls.some((call) => call.called)
            "
            :title="t('logs.pricingInfo')"
            :description="t('logs.frozenPricing')"
            compact
            ><LogPricingReceipt
              :receipt="receipt"
              :decision="log.auto_decision"
              :audit="log.request_audit"
              :total-cost="log.estimated_cost_nano_usd"
          /></AppFormSection>
          <AppFormSection v-if="admin && log.attempts.length" :title="t('logs.attempts')" compact>
            <template #actions
              ><span class="modern-log-detail-note">{{
                t('logs.attemptCount', { count: n(log.attempts.length) })
              }}</span></template
            >
            <ol class="modern-log-attempts">
              <li
                v-for="attempt in log.attempts"
                :key="attempt.sequence"
                class="modern-log-attempt"
              >
                <div class="modern-log-attempt-heading">
                  <span class="modern-log-attempt-number">{{ n(attempt.sequence) }}</span
                  ><AppChannelIcon
                    v-if="attempt.channel_id && channels?.has(attempt.channel_id)"
                    :icon="channels.get(attempt.channel_id)!.icon"
                    :name="channels.get(attempt.channel_id)!.name"
                    :mark="channels.get(attempt.channel_id)!.mark"
                    size="sm"
                    :tooltip="false"
                  /><AppOverflowText
                    class="modern-log-attempt-group"
                    :class="{
                      'is-deleted': !attempt.group_name && groups && !groups.has(attempt.group_id),
                    }"
                    :text="
                      attempt.group_name ||
                      groups?.get(attempt.group_id)?.name ||
                      (groups ? t('logs.deleted') : '—')
                    "
                  /><AppBadge
                    :tone="
                      attempt.status_code >= 400
                        ? 'danger'
                        : attempt.status_code
                          ? 'success'
                          : 'warning'
                    "
                    variant="plain"
                    size="xs"
                    >{{ attempt.status_code || t('logs.noResponse') }}</AppBadge
                  ><span>{{ logDuration(attempt.duration_ms, locale) }}</span>
                </div>
                <div class="modern-log-attempt-route">
                  <LogCredentialValue
                    :name="attempt.credential_name"
                    :group-id="attempt.group_id"
                    :credential-id="attempt.credential_id"
                    :deleted="attempt.credential_deleted"
                    :connection-type="
                      (attempt.channel_id
                        ? channels?.get(attempt.channel_id)?.connectionType
                        : undefined) ?? groups?.get(attempt.group_id)?.connectionType
                    "
                  />
                  <AppOverflowText :text="attempt.upstream_model ?? '—'" /><AppBadge
                    v-if="attempt.will_retry"
                    tone="warning"
                    variant="soft"
                    size="xs"
                    >{{ t('logs.willRetry') }}</AppBadge
                  ><AppBadge v-else-if="attempt.committed" tone="brand" variant="soft" size="xs">{{
                    t('logs.committed')
                  }}</AppBadge>
                </div>
                <p v-if="attempt.error_summary" class="modern-log-attempt-error">
                  {{ attempt.error_summary }}
                </p>
                <dl
                  v-if="
                    attempt.failure_category !== 'ok' ||
                    (attempt.effect && attempt.effect !== 'none')
                  "
                  class="modern-log-detail-grid modern-log-attempt-metrics"
                >
                  <div>
                    <dt>{{ t('logs.filters.failure_category') }}</dt>
                    <dd>{{ valueName(attempt.failure_category) }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('logs.effect') }}</dt>
                    <dd>{{ valueName(attempt.effect) }}</dd>
                  </div>
                </dl>
                <details class="modern-log-attempt-extra">
                  <summary>{{ t('logs.moreDiagnostics') }}</summary>
                  <dl class="modern-log-detail-grid">
                    <div>
                      <dt>{{ t('logs.columns.operation') }}</dt>
                      <dd>{{ valueName(attempt.operation) }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.columns.route_mode') }}</dt>
                      <dd>{{ valueName(attempt.route_mode) }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.columns.upstream_protocol') }}</dt>
                      <dd><AppProtocolTag :protocol="attempt.upstream_protocol" /></dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.dispatchState') }}</dt>
                      <dd>{{ valueName(attempt.dispatch_state) }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.responseStarted') }}</dt>
                      <dd>{{ t(attempt.response_started ? 'logs.yes' : 'logs.no') }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.failureOrigin') }}</dt>
                      <dd>{{ valueName(attempt.failure_origin) }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.failureScope') }}</dt>
                      <dd>{{ valueName(attempt.failure_scope) }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.retryDirective') }}</dt>
                      <dd>{{ valueName(attempt.retry_directive) }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.action') }}</dt>
                      <dd>{{ valueName(attempt.action) }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('logs.cooldownUntil') }}</dt>
                      <dd>
                        {{
                          attempt.cooldown_until_ms === null
                            ? '—'
                            : logTime(attempt.cooldown_until_ms, locale, true)
                        }}
                      </dd>
                    </div>
                    <div v-if="attempt.rule_id" class="is-wide">
                      <dt>{{ t('logs.matchedRule') }}</dt>
                      <dd>{{ attempt.rule_id }}</dd>
                    </div>
                    <div v-if="attempt.error_code" class="is-wide">
                      <dt>{{ t('logs.columns.error_code') }}</dt>
                      <dd>
                        <AppCopyValue :value="attempt.error_code" :label="t('logs.copyError')" />
                      </dd>
                    </div>
                    <div v-if="attempt.upstream_request_id" class="is-wide">
                      <dt>{{ t('logs.upstreamRequest') }}</dt>
                      <dd>
                        <AppCopyValue
                          :value="attempt.upstream_request_id"
                          :label="t('logs.copyRequest')"
                        />
                      </dd>
                    </div>
                    <div v-if="attempt.reasoning" class="is-wide">
                      <dt>{{ t('logs.reasoning') }}</dt>
                      <dd>
                        {{
                          [
                            valueName(attempt.reasoning.mode),
                            valueName(attempt.reasoning.effort),
                            attempt.reasoning.budget_tokens,
                          ]
                            .filter(Boolean)
                            .join(' · ')
                        }}
                      </dd>
                    </div>
                  </dl>
                </details>
              </li>
            </ol>
          </AppFormSection>
        </template>
      </div>
      <footer v-if="log" class="modern-log-detail-footer">
        <AppButton
          v-if="admin && log.group_id && groups?.has(log.group_id)"
          as-child
          variant="ghost"
          size="xs"
          ><RouterLink :to="{ name: 'modern-group-detail', params: { id: log.group_id } }">{{
            t('logs.viewGroup')
          }}</RouterLink></AppButton
        >
        <AppButton
          v-if="admin && log.access_key.id && !log.access_key.deleted"
          as-child
          variant="ghost"
          size="xs"
          ><RouterLink
            :to="{
              name: 'modern-access-keys',
              query: { panel: 'detail', access_key: String(log.access_key.id) },
            }"
            >{{ t('logs.viewAccessKey') }}</RouterLink
          ></AppButton
        >
        <AppButton as-child variant="brand" size="xs"
          ><RouterLink :to="usageLocation">{{ t('logs.viewUsage') }}</RouterLink></AppButton
        >
        <AppCopyValue
          :value="log.request_id"
          :resolve-value="resolveRedactedLog"
          @copied="messages.show({ tone: 'success', text: t('logs.redactedCopySuccess') })"
          @failed="messages.show({ tone: 'danger', text: t('logs.redactedCopyFailed') })"
        >
          <template #trigger="{ copy, pending }">
            <AppButton
              :icon="ShieldCheck"
              :loading="pending"
              variant="ghost"
              size="xs"
              @click="copy()"
            >
              {{ t('logs.copyRedactedLog') }}
            </AppButton>
          </template>
        </AppCopyValue>
      </footer>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-log-detail-body {
  container: modern-log-detail / inline-size;
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
  gap: var(--modern-space-4);
  padding: var(--modern-space-4) var(--modern-space-5);
}
.modern-log-result {
  display: grid;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-top: var(--modern-focus-width) solid var(--modern-accent);
  border-radius: var(--modern-radius-panel);
  background: linear-gradient(var(--modern-key-card-tint), var(--modern-surface) 45%);
}
.modern-log-result--success {
  border-top-color: var(--modern-success);
}
.modern-log-result--danger {
  border-top-color: var(--modern-danger);
}
.modern-log-result--warning {
  border-top-color: var(--modern-warning);
}
.modern-log-result-context {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-3);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-result-model {
  flex: 1;
  min-width: 0;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-log-primary-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--modern-space-3);
  margin: 0;
  padding: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-surface);
}
.modern-log-primary-metrics > div {
  min-width: 0;
  display: grid;
  gap: var(--modern-space-1);
}
.modern-log-primary-metrics dt {
  display: flex;
  gap: var(--modern-space-1-5);
  align-items: center;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  letter-spacing: var(--modern-tracking-label);
}
.modern-log-primary-metrics dd {
  margin: 0;
  color: var(--modern-text);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-log-result-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-2) var(--modern-space-4);
  margin: 0;
}
.modern-log-result-meta > div {
  display: grid;
  grid-template-columns: 4.5em minmax(0, 1fr);
  align-items: baseline;
  gap: var(--modern-space-2);
  min-width: 0;
  font-size: var(--modern-font-size-small);
}
.modern-log-result-meta dt {
  color: var(--modern-muted);
  overflow-wrap: anywhere;
}
.modern-log-result-meta dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
  font-variant-numeric: tabular-nums;
}
.modern-log-stream-rate {
  color: var(--modern-muted);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-caption);
  white-space: nowrap;
}
.modern-log-error.is-note {
  background: var(--modern-subtle);
  border-left-color: var(--modern-border);
  color: var(--modern-muted);
}
.is-deleted {
  color: var(--modern-muted);
}
.modern-log-detail-body :deep(.modern-form-section-heading h3) {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-log-detail-body :deep(.modern-form-section-heading h3::before) {
  content: '';
  width: var(--modern-space-0-5);
  height: var(--modern-space-3);
  border-radius: var(--modern-radius-small);
  background: var(--modern-accent);
}
.modern-log-result-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
}
.modern-log-detail-note {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-request-identity {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-request-identity > :first-child {
  flex: none;
}
.modern-log-request-identity > :last-child {
  min-width: 0;
}
.modern-log-error {
  display: grid;
  gap: var(--modern-space-1);
  border-left: var(--modern-focus-width) solid var(--modern-danger);
  border-radius: var(--modern-radius-small);
  background: var(--modern-danger-soft);
  color: var(--modern-danger);
  padding: var(--modern-space-2) var(--modern-space-3);
  font-size: var(--modern-font-size-small);
  overflow-wrap: anywhere;
}
.modern-log-error p {
  white-space: pre-wrap;
}
.modern-log-detail-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(216px, 100%), 1fr));
  gap: var(--modern-space-2) var(--modern-space-4);
  margin: 0;
}
/* 标签列定宽右对齐：每行标签与取值都贴合，整个面板只有一条竖向基线。
   非中文标签更长，允许换行而不是溢出。 */
/* 取值里混着纯文本、20px 协议标签和 24px 渠道图标，基线对齐会被图标压低，
   同一行的标签高低不齐；统一按行居中并锁定行高。 */
.modern-log-detail-grid > div {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  min-height: var(--modern-space-6);
}
.modern-log-detail-grid dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  text-align: right;
  overflow-wrap: break-word;
}
.modern-log-detail-grid dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
}
/* 纯数值分段用等宽字，和列表里的数字列保持一致。 */
.modern-log-detail-grid.is-numeric dd {
  font-family: var(--modern-font-mono);
}
/* 整行字段（错误代码、上游请求 ID 等）取值会换行，且不含图标，
   用基线让标签跟住首行，而不是被居中挤到多行的正中间。 */
.modern-log-detail-grid .is-wide {
  grid-column: 1 / -1;
  align-items: baseline;
}
.modern-log-attempts {
  list-style: none;
  margin: 0;
  padding: 0;
}
.modern-log-attempt {
  display: grid;
  gap: var(--modern-space-2);
  position: relative;
  padding: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-surface);
}
.modern-log-attempt + .modern-log-attempt {
  margin-top: var(--modern-space-2);
}

.modern-log-attempt-heading {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-small);
}
.modern-log-attempt-group {
  flex: 1;
  font-weight: var(--modern-weight-medium);
}
.modern-log-attempt-heading > :last-child {
  color: var(--modern-muted);
  flex: none;
}
.modern-log-attempt-number {
  display: grid;
  place-items: center;
  flex: none;
  width: var(--modern-control-xxs);
  height: var(--modern-control-xxs);
  border-radius: var(--modern-radius-small);
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-variant-numeric: tabular-nums;
}
.modern-log-attempt-route {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-2);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-attempt-route > :first-child {
  overflow-wrap: anywhere;
}
.modern-log-attempt-route > :nth-child(2) {
  max-width: 28ch;
}
.modern-log-attempt-error {
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.modern-log-attempt-extra {
  display: grid;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-attempt-extra summary {
  cursor: pointer;
  width: fit-content;
  border-radius: var(--modern-radius-small);
  color: var(--modern-accent);
  padding: var(--modern-space-0-5) var(--modern-space-1);
  margin-inline-start: calc(-1 * var(--modern-space-1));
}
.modern-log-attempt-extra summary:hover {
  background: var(--modern-control-hover);
}
.modern-log-attempt-extra summary:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-space-0-5);
}
.modern-log-attempt-extra[open] > :not(summary) {
  margin-top: var(--modern-space-3);
}
.modern-log-detail-footer {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-1);
  padding: var(--modern-space-3) var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-log-detail-footer > :deep(.modern-button) {
  width: auto;
  flex: 1 1 auto;
  white-space: nowrap;
}
@container modern-log-detail (max-width: 400px) {
  .modern-log-detail-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
@container modern-log-detail (max-width: 480px) {
  .modern-log-primary-metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@container modern-log-detail (max-width: 360px) {
  .modern-log-result-meta {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
