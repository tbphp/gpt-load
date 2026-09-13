<script setup lang="ts">
import { Activity, ArrowDownToLine, Clock3, Coins } from '@lucide/vue'
import { protocolLabel } from '@modern/i18n/protocols'
import { timeRangeQuery } from '@modern/app/time-range'
import type { DateRangePreset } from '@modern/components/ui/date-time'
import { useQuery } from '@tanstack/vue-query'
import { DialogRoot } from 'reka-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { getLogDetail, logDetailKey } from '@modern/api/logs'
import type { GroupRow } from '@modern/api/groups'
import type { GroupChannel } from '@modern/api/group-create'
import { useMessageSource } from '@modern/app/messages'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppCopyValue,
  AppDialogContent,
  AppDialogHeader,
  AppFormSection,
  AppOverflowText,
  AppIcon,
  AppChannelIcon,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import type { LogColumnId } from './log-columns'
import { logDuration, logStatusTone, logTime } from './log-display'
import LogPricingReceipt from './LogPricingReceipt.vue'
import LogValue from './LogValue.vue'

const props = defineProps<{
  id: string
  admin: boolean
  groups: ReadonlyMap<number, GroupRow>
  channels: ReadonlyMap<string, GroupChannel>
  from: string
  to: string
  preset?: DateRangePreset
}>()
defineEmits<{ close: [] }>()
const { t, te, n, locale } = useI18n()
const client = useApiClient()
const query = useQuery({
  queryKey: [...logDetailKey(props.id), props.admin],
  queryFn: ({ signal }) => getLogDetail(client, props.id, signal),
})
const log = computed(() => query.data.value)
const outcomeFields = computed<LogColumnId[]>(() => [
  'status_code',
  'stream',
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
const requestFields: LogColumnId[] = [
  'operation',
  'reasoning_mode',
  'reasoning_effort',
  'reasoning_budget',
]
const usageFields: LogColumnId[] = [
  'input_tokens',
  'output_tokens',
  'cache_read_tokens',
  'cache_hit_rate',
  'cache_write_5m_tokens',
  'cache_write_1h_tokens',
  'cache_write_unknown_tokens',
  'total_tokens',
  'usage_state',
  'estimated_cost_nano_usd',
  'cost_state',
  'pricing_completeness',
  'pricing_mode',
  'context_threshold_tokens',
]
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
            <div class="modern-log-result-heading">
              <AppBadge :tone="logStatusTone[log.status]" dot>{{
                t('logs.values.' + log.status)
              }}</AppBadge
              ><time>{{ logTime(log.completed_at_ms, locale, true) }}</time>
            </div>
            <div class="modern-log-model-heading">
              <AppOverflowText :text="log.client_model || '—'" /><AppBadge
                tone="brand"
                variant="outline"
                >{{ protocolLabel(log.protocol, t) }}</AppBadge
              >
            </div>
            <div class="modern-log-request-identity">
              <span>{{ t('logs.columns.request_id') }}</span
              ><AppCopyValue :value="log.request_id" :label="t('logs.copyRequest')" />
            </div>
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
            <dl class="modern-log-detail-grid modern-log-result-meta">
              <div v-for="field in outcomeFields" :key="field">
                <dt>{{ t('logs.columns.' + field) }}</dt>
                <dd>
                  <LogValue :row="log" :column="field" :groups="groups" :channels="channels" />
                </dd>
              </div>
            </dl>
          </section>
          <AppFormSection :title="t('logs.requestInfo')" compact>
            <dl class="modern-log-detail-grid">
              <div v-for="field in requestFields" :key="field">
                <dt>{{ t('logs.columns.' + field) }}</dt>
                <dd>
                  <LogValue :row="log" :column="field" :groups="groups" :channels="channels" />
                </dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection v-if="admin" :title="t('logs.routingInfo')" compact>
            <dl class="modern-log-detail-grid">
              <div
                v-for="field in routingFields"
                :key="field"
                :class="{
                  'is-wide': ['upstream_model', 'upstream_reported_model'].includes(field),
                }"
              >
                <dt>{{ t('logs.columns.' + field) }}</dt>
                <dd>
                  <LogValue :row="log" :column="field" :groups="groups" :channels="channels" />
                </dd>
              </div>
            </dl>
          </AppFormSection>
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
                    v-if="attempt.channel_id && channels.has(attempt.channel_id)"
                    :icon="channels.get(attempt.channel_id)!.icon"
                    :name="channels.get(attempt.channel_id)!.name"
                    :mark="channels.get(attempt.channel_id)!.mark"
                    size="sm"
                  /><AppOverflowText
                    class="modern-log-attempt-group"
                    :class="{ 'is-deleted': !attempt.group_name }"
                    :text="attempt.group_name || t('logs.deleted')"
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
                  <span :class="{ 'is-deleted': !attempt.credential_name }">{{
                    attempt.credential_name || t('logs.deleted')
                  }}</span
                  ><AppOverflowText :text="attempt.upstream_model ?? '—'" /><AppBadge
                    v-if="attempt.will_retry"
                    tone="warning"
                    variant="plain"
                    size="xs"
                    >{{ t('logs.willRetry') }}</AppBadge
                  ><AppBadge v-else-if="attempt.committed" tone="brand" variant="plain" size="xs">{{
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
                      <dd>{{ protocolLabel(attempt.upstream_protocol, t) }}</dd>
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
                  <LogPricingReceipt
                    v-if="attempt.pricing_receipt"
                    :receipt="attempt.pricing_receipt"
                  />
                </details>
              </li>
            </ol>
          </AppFormSection>
          <AppFormSection :title="t('logs.usageInfo')" compact>
            <dl class="modern-log-detail-grid">
              <div v-for="field in usageFields" :key="field">
                <dt>{{ t('logs.columns.' + field) }}</dt>
                <dd>
                  <LogValue :row="log" :column="field" :groups="groups" :channels="channels" />
                </dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection
            v-if="receipt"
            :title="t('logs.pricingInfo')"
            :description="t('logs.frozenPricing')"
            compact
            ><LogPricingReceipt :receipt="receipt"
          /></AppFormSection>
        </template>
      </div>
      <footer v-if="log" class="modern-log-detail-footer">
        <AppButton
          v-if="admin && log.group_id && groups.has(log.group_id)"
          as-child
          variant="ghost"
          size="xs"
          ><RouterLink :to="{ name: 'modern-group-detail', params: { id: log.group_id } }">{{
            t('logs.viewGroup')
          }}</RouterLink></AppButton
        >
        <AppButton
          v-if="
            admin &&
            log.group_id &&
            groups.has(log.group_id) &&
            log.credential_id &&
            log.credential_name
          "
          as-child
          variant="ghost"
          size="xs"
          ><RouterLink
            :to="{
              name: 'modern-group-detail',
              params: { id: log.group_id },
              query: { credential: String(log.credential_id) },
            }"
            >{{ t('logs.viewCredential') }}</RouterLink
          ></AppButton
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
.modern-log-model-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-log-model-heading > :first-child {
  flex: 1 1 180px;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-log-primary-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
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
  font-size: var(--modern-font-size-secondary);
}
.modern-log-primary-metrics dd {
  margin: 0;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
  font-variant-numeric: tabular-nums;
}
.modern-log-result-meta {
  padding-inline: var(--modern-space-1);
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
.modern-log-result-heading time,
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
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-2) var(--modern-space-4);
  margin: 0;
  font-size: var(--modern-font-size-small);
}
.modern-log-detail-grid > div {
  display: grid;
  grid-template-columns: minmax(72px, max-content) minmax(0, 1fr);
  align-items: baseline;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-log-detail-grid dt {
  color: var(--modern-muted);
}
.modern-log-detail-grid dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--modern-text);
  font-variant-numeric: tabular-nums;
}
.modern-log-detail-grid .is-wide {
  grid-column: 1 / -1;
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
@container modern-log-detail (max-width: 400px) {
  .modern-log-detail-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
