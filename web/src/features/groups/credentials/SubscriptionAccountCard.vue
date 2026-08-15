<script setup lang="ts">
import { CircleCheck, CircleOff, Ellipsis, LogIn, RotateCcw, Trash2 } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialItemDto, CredentialQuotaWindowDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import IconButton from '@/components/ui/IconButton.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatEstimatedCost, formatTokens } from '@/lib/format'

import { presentCredentialFailureCategory } from './credential-failure-presenter'

const props = defineProps<{
  item: CredentialItemDto
  busy: boolean
}>()
const emit = defineEmits<{
  toggle: [item: CredentialItemDto]
  restore: [item: CredentialItemDto]
  refresh: [item: CredentialItemDto]
  reset: [item: CredentialItemDto]
  reauthorize: [item: CredentialItemDto]
  remove: [item: CredentialItemDto]
}>()
const { locale, n, t } = useI18n()
const menuOpen = ref(false)
const nowMs = ref(Date.now())
let freshnessTimer: number | undefined

onMounted(() => {
  freshnessTimer = window.setInterval(() => {
    nowMs.value = Date.now()
  }, 30_000)
})
onBeforeUnmount(() => {
  if (freshnessTimer !== undefined) window.clearInterval(freshnessTimer)
})

const observation = computed(() => props.item.observation)
const snapshot = computed(() => observation.value?.snapshot)
// 窗口按时长从短到长排：最快恢复的排最前，用户先看到的是最快能缓解的那一条。
// 缺 window_seconds 的窗口（上游未提供时长）排在最后，保持后端给的相对次序。
const quotaWindows = computed(() =>
  [...(snapshot.value?.quota_windows ?? [])].sort((left, right) => {
    const leftSeconds = left.window_seconds ?? Number.MAX_SAFE_INTEGER
    const rightSeconds = right.window_seconds ?? Number.MAX_SAFE_INTEGER
    return leftSeconds - rightSeconds
  }),
)
const accountQuotaWindows = computed(() =>
  quotaWindows.value.filter((window) => window.scope === 'account'),
)
const lastUsedAtMS = computed(() => {
  const values = accountQuotaWindows.value
    .map((window) => window.observed_usage?.last_used_at_ms)
    .filter((value): value is number => value !== undefined)
  return values.length ? Math.max(...values) : undefined
})
const constrainedModels = computed(() =>
  Array.from(new Set(quotaWindows.value.flatMap((window) => window.model_ids ?? []))),
)
const resetCreditsAvailable = computed(() => snapshot.value?.reset_credits_available ?? 0)
const resetCredits = computed(() => snapshot.value?.reset_credits ?? [])
const nextResetCredit = computed(() =>
  resetCredits.value.find(({ expires_at_ms }) => expires_at_ms > nowMs.value),
)
const observationIsCurrent = computed(() => {
  const freshUntil = observation.value?.fresh_until_ms
  return (
    observation.value?.state === 'fresh' &&
    freshUntil !== null &&
    freshUntil !== undefined &&
    freshUntil > nowMs.value
  )
})
const resetCreditsUsable = computed(() => {
  if (!observationIsCurrent.value || !resetCreditsAvailable.value) return false
  return resetCredits.value.length === 0 || nextResetCredit.value !== undefined
})
const isProblem = computed(
  () => props.item.effective_status === 'cooldown' || props.item.effective_status === 'blacklisted',
)

function quotaWindowIsCurrent(window: CredentialQuotaWindowDto): boolean {
  if (!observationIsCurrent.value) return false
  return window.reset_at_ms === undefined || window.reset_at_ms > nowMs.value
}

type UnifiedStatus =
  | 'available'
  | 'quota_exhausted'
  | 'cooldown'
  | 'blacklisted'
  | 'refreshing'
  | 'needs_reauth'
  | 'disabled'

const unifiedStatus = computed<UnifiedStatus>(() => {
  if (props.item.configured_status === 'disabled') return 'disabled'
  if (props.item.auth_state === 'refreshing') return 'refreshing'
  if (
    props.item.auth_state === 'reauthorization_required' ||
    props.item.auth_state === 'outcome_unknown'
  ) {
    return 'needs_reauth'
  }
  if (props.item.effective_status === 'blacklisted') return 'blacklisted'
  if (props.item.effective_status === 'cooldown') return 'cooldown'
  if (
    quotaWindows.value.some(
      (window) =>
        window.scope === 'account' && quotaWindowIsCurrent(window) && window.state === 'exhausted',
    )
  ) {
    return 'quota_exhausted'
  }
  return 'available'
})
const statusTone = computed<'success' | 'warning' | 'danger' | 'neutral'>(() => {
  const tones: Record<UnifiedStatus, 'success' | 'warning' | 'danger' | 'neutral'> = {
    available: 'success',
    quota_exhausted: 'warning',
    cooldown: 'warning',
    blacklisted: 'danger',
    refreshing: 'neutral',
    needs_reauth: 'danger',
    disabled: 'neutral',
  }
  return tones[unifiedStatus.value]
})
const authErrorKeys: Readonly<Record<string, string>> = {
  refresh_rejected: 'group.credentials.subscription.authError.refreshRejected',
  refresh_identity_changed: 'group.credentials.subscription.authError.identityChanged',
  refresh_outcome_unknown: 'group.credentials.subscription.authError.outcomeUnknown',
  refresh_persist_failed: 'group.credentials.subscription.authError.persistFailed',
  refresh_commit_failed: 'group.credentials.subscription.authError.persistFailed',
  refresh_registry_mismatch: 'group.credentials.subscription.authError.runtimeMismatch',
  refresh_start_failed: 'group.credentials.subscription.authError.refreshStartFailed',
  refresh_state_commit_failed: 'group.credentials.subscription.authError.persistFailed',
}
const authIssue = computed(() => {
  if (
    props.item.auth_state !== 'reauthorization_required' &&
    props.item.auth_state !== 'outcome_unknown'
  ) {
    return ''
  }
  const key = props.item.auth_error_code ? authErrorKeys[props.item.auth_error_code] : undefined
  return key ? t(key) : t(`group.credentials.subscription.auth.${props.item.auth_state}`)
})
// 卡片给的是请求日志里的 24 小时结果分布，不是 health 那个 5 分钟内存窗口
//（后者重启即清零，是给调度判定用的）。始终并排给出成功与失败：失败 0 本身是
// 信息，也让这一行宽度稳定。
const dailyUsage = computed(() => props.item.daily_usage)
// 请求日志留存短于 24 小时时计数会偏低，只用 title 提示，不占版面。
const dailyIncompleteHint = computed(() =>
  dailyUsage.value && !dailyUsage.value.data_complete
    ? t('group.credentials.subscription.dailyIncomplete')
    : undefined,
)
const failureLabel = computed(() =>
  props.item.recent_failure_count === 0
    ? t('group.credentials.none')
    : `${presentCredentialFailureCategory(t, props.item.last_failure_category)}${
        props.item.last_status_code === null ? '' : ` · ${props.item.last_status_code}`
      }`,
)
const observationErrorKeys: Readonly<Record<string, string>> = {
  observation_upstream_failed: 'group.credentials.subscription.observationError.upstreamFailed',
  observation_payload_invalid: 'group.credentials.subscription.observationError.payloadInvalid',
}
function observationErrorLabel(code: string | undefined): string {
  if (!code) return '—'
  const key = observationErrorKeys[code]
  return key ? t(key) : code
}

function remainingPercent(window: CredentialQuotaWindowDto): number | undefined {
  if (!quotaWindowIsCurrent(window)) return undefined
  if (window.utilization !== undefined) return Math.round((1 - window.utilization) * 100)
  if (window.remaining !== undefined && window.limit && window.limit > 0) {
    return Math.max(0, Math.min(100, Math.round((window.remaining / window.limit) * 100)))
  }
  return undefined
}
function quotaValueLabel(window: CredentialQuotaWindowDto): string {
  if (!quotaWindowIsCurrent(window)) return t('group.credentials.subscription.quotaPendingRefresh')
  const value = remainingPercent(window)
  if (value !== undefined)
    return t('group.credentials.subscription.remainingPercent', { value: n(value) })
  if (window.remaining !== undefined && window.limit !== undefined) {
    return t('group.credentials.subscription.remaining', {
      remaining: n(window.remaining),
      limit: n(window.limit),
    })
  }
  return t('group.credentials.subscription.unknown')
}
function usedPercentLabel(window: CredentialQuotaWindowDto): string {
  const remaining = remainingPercent(window)
  return remaining === undefined
    ? ''
    : t('group.credentials.subscription.estimate.usedPercent', { value: n(100 - remaining) })
}
function quotaTone(window: CredentialQuotaWindowDto): 'success' | 'warning' | 'danger' | undefined {
  if (!quotaWindowIsCurrent(window)) return undefined
  if (window.state === 'exhausted') return 'danger'
  const value = remainingPercent(window)
  if (value === undefined) return undefined
  if (value < 30) return 'danger'
  if (value < 70) return 'warning'
  return 'success'
}

function runMenuAction(action: 'reauthorize' | 'toggle' | 'restore' | 'remove'): void {
  menuOpen.value = false
  switch (action) {
    case 'reauthorize':
      emit('reauthorize', props.item)
      return
    case 'toggle':
      emit('toggle', props.item)
      return
    case 'restore':
      emit('restore', props.item)
      return
    case 'remove':
      emit('remove', props.item)
  }
}
</script>

<template>
  <article class="subscription-account" :class="`subscription-account--${statusTone}`">
    <div class="subscription-account__top">
      <strong class="subscription-account__mail">{{ item.mask }}</strong>
      <span v-if="snapshot?.plan_summary.name" class="subscription-account__plan">
        {{ snapshot.plan_summary.name }}
      </span>
      <StatusBadge :tone="statusTone" size="compact">
        {{ t(`group.credentials.subscription.status.${unifiedStatus}`) }}
      </StatusBadge>
      <span class="subscription-account__spacer"></span>
      <AppButton
        variant="secondary"
        size="compact"
        :busy="busy"
        :disabled="item.configured_status === 'disabled'"
        @click="emit('refresh', item)"
      >
        {{ t('group.credentials.subscription.sync') }}
      </AppButton>
      <AppPopover
        v-model:open="menuOpen"
        align="end"
        content-class="app-popover__content--account-menu"
      >
        <template #trigger>
          <IconButton
            variant="ghost"
            size="compact"
            :label="t('group.credentials.subscription.moreActions')"
            :disabled="busy"
          >
            <Ellipsis :size="16" aria-hidden="true" />
          </IconButton>
        </template>
        <div class="subscription-account__menu">
          <button type="button" :disabled="busy" @click="runMenuAction('reauthorize')">
            <LogIn :size="15" aria-hidden="true" />{{
              t('group.credentials.subscription.reauthorize')
            }}
          </button>
          <button type="button" :disabled="busy" @click="runMenuAction('toggle')">
            <CircleOff v-if="item.configured_status === 'active'" :size="15" aria-hidden="true" />
            <CircleCheck v-else :size="15" aria-hidden="true" />
            {{
              item.configured_status === 'active'
                ? t('group.credentials.disable')
                : t('group.credentials.enable')
            }}
          </button>
          <button v-if="isProblem" type="button" :disabled="busy" @click="runMenuAction('restore')">
            <RotateCcw :size="15" aria-hidden="true" />{{ t('group.credentials.restore') }}
          </button>
          <div class="subscription-account__menu-divider"></div>
          <button
            type="button"
            class="subscription-account__menu-danger"
            :disabled="busy"
            @click="runMenuAction('remove')"
          >
            <Trash2 :size="15" aria-hidden="true" />{{ t('group.credentials.delete') }}
          </button>
        </div>
      </AppPopover>
    </div>

    <div v-if="authIssue" class="subscription-account__alert">
      <span>{{ authIssue }}</span>
      <AppButton size="compact" :disabled="busy" @click="emit('reauthorize', item)">
        {{ t('group.credentials.subscription.reauthorize') }}
      </AppButton>
    </div>

    <div v-if="quotaWindows.length" class="subscription-account__quotas">
      <div v-for="window in quotaWindows" :key="window.id" class="subscription-account__quota">
        <OverflowTooltip class="subscription-account__quota-name" :content="window.label">
          {{ window.label }}
        </OverflowTooltip>
        <span
          v-if="remainingPercent(window) !== undefined"
          class="subscription-account__quota-track"
          :class="
            quotaTone(window) ? `subscription-account__quota-track--${quotaTone(window)}` : ''
          "
          role="progressbar"
          :aria-label="window.label"
          :aria-valuenow="remainingPercent(window)"
          :aria-valuetext="quotaValueLabel(window)"
          aria-valuemin="0"
          aria-valuemax="100"
        >
          <span
            class="subscription-account__quota-fill"
            :class="
              quotaTone(window) ? `subscription-account__quota-fill--${quotaTone(window)}` : ''
            "
            :style="{ width: `${remainingPercent(window)}%` }"
          />
        </span>
        <span
          v-else
          class="subscription-account__quota-track subscription-account__quota-track--unknown"
          role="img"
          :aria-label="`${window.label}: ${quotaValueLabel(window)}`"
        />
        <span class="subscription-account__quota-value">{{ quotaValueLabel(window) }}</span>
        <span class="subscription-account__quota-reset">
          <template v-if="window.reset_at_ms">
            <AppRelativeTime
              :instant="window.reset_at_ms"
              :locale="locale"
              :empty-label="t('group.credentials.subscription.unknown')"
              hint
            />
          </template>
          <template v-else>—</template>
        </span>
      </div>
    </div>
    <p v-else class="subscription-account__faint">
      {{ t('group.credentials.subscription.noQuota') }}
    </p>

    <p v-if="unifiedStatus === 'quota_exhausted'" class="subscription-account__hint">
      {{ t('group.credentials.subscription.quotaExhaustedHint') }}
    </p>

    <div
      v-if="resetCreditsUsable"
      class="subscription-account__credits"
      :class="{
        'subscription-account__credits--urgent': unifiedStatus === 'quota_exhausted',
      }"
    >
      <span>{{ t('group.credentials.subscription.resetCredits') }}</span>
      <span class="subscription-account__credits-dots" aria-hidden="true">
        <i v-for="index in Math.min(resetCreditsAvailable, 5)" :key="index"></i>
      </span>
      <strong>{{
        t('group.credentials.subscription.resetCreditsCount', { count: n(resetCreditsAvailable) })
      }}</strong>
      <span v-if="nextResetCredit" class="subscription-account__credits-expiry">
        {{ t('group.credentials.subscription.nearestResetCredit') }}
        <AppRelativeTime
          :instant="nextResetCredit.expires_at_ms"
          :locale="locale"
          :empty-label="t('group.credentials.subscription.unknown')"
          hint
        />
      </span>
      <span class="subscription-account__spacer"></span>
      <AppButton size="compact" :disabled="busy" @click="emit('reset', item)">
        {{ t('group.credentials.subscription.consumeResetCredit') }}
      </AppButton>
    </div>

    <!-- 一行读完「最近跑得怎么样」：计数窗口与 API Key 列表的「近 5 分钟」列同源。
         访问凭据到期这类低频信息下沉到诊断信息，不占这一行。 -->
    <div class="subscription-account__meta">
      <span v-if="lastUsedAtMS" class="subscription-account__meta-last-used">
        {{ t('group.credentials.subscription.lastUsed') }}
        <AppRelativeTime
          :instant="lastUsedAtMS"
          :locale="locale"
          :empty-label="t('group.credentials.subscription.unknown')"
          hint
        />
      </span>
      <span v-if="dailyUsage" class="subscription-account__meta-daily">
        <span class="subscription-account__meta-window">
          {{ t('group.credentials.subscription.dailyWindow') }}
        </span>
        <span class="subscription-account__daily-outcomes" :title="dailyIncompleteHint">
          <span>
            {{ t('group.credentials.subscription.dailySuccess') }}
            <strong class="subscription-account__daily-success">{{
              n(dailyUsage.success_count)
            }}</strong>
          </span>
          <span class="subscription-account__daily-separator" aria-hidden="true">·</span>
          <span>
            {{ t('group.credentials.subscription.dailyFailure') }}
            <strong class="subscription-account__daily-failure">{{
              n(dailyUsage.failure_count)
            }}</strong>
          </span>
        </span>
      </span>
      <span class="subscription-account__meta-sync">
        {{ t('group.credentials.subscription.synced') }}
        <AppRelativeTime
          :instant="observation?.observed_at_ms ?? null"
          :locale="locale"
          :empty-label="t('group.credentials.subscription.unknown')"
          hint
        />
      </span>
    </div>

    <details v-if="accountQuotaWindows.length" class="subscription-account__estimate">
      <summary>
        {{ t('group.credentials.subscription.estimate.title') }} ·
        {{
          accountQuotaWindows
            .map((window) => window.label)
            .join(t('group.credentials.subscription.estimate.windowSeparator'))
        }}
      </summary>
      <div class="subscription-account__estimate-grid">
        <article v-for="window in accountQuotaWindows" :key="window.id">
          <div class="subscription-account__estimate-head">
            <strong>{{ window.label }}</strong>
            <span v-if="usedPercentLabel(window)">{{ usedPercentLabel(window) }}</span>
          </div>
          <dl v-if="window.observed_usage" class="subscription-account__estimate-rows">
            <div>
              <dt>{{ t('group.credentials.subscription.estimate.requests') }}</dt>
              <dd>{{ n(window.observed_usage.request_count) }}</dd>
            </div>
            <div>
              <dt>{{ t('group.credentials.subscription.estimate.tokens') }}</dt>
              <dd>{{ formatTokens(window.observed_usage.total_tokens, locale) }}</dd>
            </div>
            <div>
              <dt>{{ t('group.credentials.subscription.estimate.referenceCost') }}</dt>
              <dd class="subscription-account__estimate-money">
                {{
                  formatEstimatedCost(
                    window.observed_usage.estimated_reference_cost_nano_usd,
                    locale,
                  )
                }}
              </dd>
            </div>
          </dl>
        </article>
      </div>
    </details>

    <details class="subscription-account__diagnostics">
      <summary>{{ t('group.credentials.subscription.diagnostics') }}</summary>
      <dl class="subscription-account__diagnostics-grid">
        <div>
          <dt>{{ t('group.credentials.detailsFailure') }}</dt>
          <dd>{{ failureLabel }}</dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.detailsConsecutive') }}</dt>
          <dd>{{ n(item.consecutive_failure_count) }}</dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.tokenExpiresAt') }}</dt>
          <dd>
            <AppRelativeTime
              :instant="item.account.expires_at_ms ?? null"
              :locale="locale"
              :empty-label="t('group.credentials.subscription.unknown')"
              hint
            />
            <template v-if="item.account.expires_at_ms && item.auth_state === 'ready'">
              · {{ t('group.credentials.subscription.autoRenews') }}
            </template>
          </dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.lastTokenRefresh') }}</dt>
          <dd>
            <AppRelativeTime
              :instant="item.account.last_refresh_at_ms ?? null"
              :locale="locale"
              :empty-label="t('group.credentials.subscription.unknown')"
              hint
            />
          </dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.freshUntil') }}</dt>
          <dd>
            <AppRelativeTime
              :instant="observation?.fresh_until_ms ?? null"
              :locale="locale"
              :empty-label="t('group.credentials.subscription.unknown')"
              hint
            />
          </dd>
        </div>
        <div>
          <dt>{{ t('group.credentials.subscription.lastError') }}</dt>
          <dd>{{ observationErrorLabel(observation?.last_error_code) }}</dd>
        </div>
      </dl>
      <div v-if="constrainedModels.length" class="subscription-account__models">
        <span>{{ t('group.credentials.subscription.modelConstraints') }}</span>
        <code v-for="model in constrainedModels" :key="model">{{ model }}</code>
      </div>
      <div v-if="resetCredits.length" class="subscription-account__reset-credit-list">
        <span>{{ t('group.credentials.subscription.resetCreditExpirations') }}</span>
        <AppRelativeTime
          v-for="credit in resetCredits"
          :key="credit.expires_at_ms"
          :instant="credit.expires_at_ms"
          :locale="locale"
          :empty-label="t('group.credentials.subscription.unknown')"
          hint
        />
      </div>
    </details>
  </article>
</template>

<style scoped>
.subscription-account {
  display: grid;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-left: 3px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  box-shadow: var(--shadow-card);
  padding: var(--space-3) var(--space-4);
}
.subscription-account--success {
  border-left-color: var(--color-success);
}
.subscription-account--warning {
  border-left-color: var(--color-warning);
}
.subscription-account--danger {
  border-left-color: var(--color-danger);
}
.subscription-account__top {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}
/* 卡片改成一行两个后宽度减半，长掩码要能收住而不是把徽标和按钮顶出去 */
.subscription-account__mail {
  min-width: 0;
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-body);
  font-weight: 620;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-account__plan {
  border-radius: var(--radius-tag);
  background: var(--color-action-soft);
  color: var(--color-action);
  padding: 2px 7px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  text-transform: uppercase;
}
.subscription-account__spacer {
  flex: 1 1 auto;
}
.subscription-account__quotas {
  display: grid;
  gap: var(--space-2);
}
.subscription-account__quota {
  display: grid;
  grid-template-columns: minmax(80px, 112px) minmax(0, 1fr) max-content max-content;
  align-items: center;
  gap: var(--space-3);
}
.subscription-account__quota-name {
  overflow: hidden;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 580;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-account__quota-track {
  position: relative;
  height: 7px;
  border-radius: 999px;
  background: light-dark(#e9edf1, #29313a);
  overflow: hidden;
}
.subscription-account__quota-track--success {
  background: light-dark(#dff6e6, #1b3c29);
}
.subscription-account__quota-track--warning {
  background: light-dark(#fff4cc, #3b310f);
}
.subscription-account__quota-track--danger {
  background: light-dark(#ffe5e8, #421d25);
}
.subscription-account__quota-fill {
  position: absolute;
  inset: 0 auto 0 0;
  border-radius: inherit;
  background: #42be65;
}
.subscription-account__quota-fill--warning {
  background: #f1c21b;
}
.subscription-account__quota-fill--danger {
  background: #fa4d56;
}
.subscription-account__quota-track--unknown {
  background: repeating-linear-gradient(
    135deg,
    var(--color-border-subtle),
    var(--color-border-subtle) 6px,
    var(--color-surface-sunken) 6px,
    var(--color-surface-sunken) 12px
  );
}
.subscription-account__quota-value {
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 620;
  font-variant-numeric: tabular-nums;
  text-align: right;
}
.subscription-account__quota-reset {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
  text-align: right;
}
.subscription-account__faint,
.subscription-account__hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.subscription-account__estimate,
.subscription-account__diagnostics {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-2);
}
.subscription-account__estimate summary,
.subscription-account__diagnostics summary {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  cursor: pointer;
}
.subscription-account__estimate-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: var(--space-2);
  margin-top: var(--space-2);
}
.subscription-account__estimate-grid article {
  display: grid;
  gap: 5px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 9px 10px;
}
.subscription-account__estimate-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}
.subscription-account__estimate-head strong {
  font-size: var(--text-sm);
}
.subscription-account__estimate-head span {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}
.subscription-account__estimate-rows {
  display: grid;
  gap: 3px;
  margin: 0;
}
.subscription-account__estimate-rows > div {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: baseline;
  gap: var(--space-2);
}
.subscription-account__estimate-rows dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-account__estimate-rows dd {
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 620;
  font-variant-numeric: tabular-nums;
  text-align: right;
}
.subscription-account__estimate-money {
  color: var(--color-text-muted);
  font-weight: 500;
}
.subscription-account__credits {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 7px 10px;
  font-size: var(--text-sm);
}
.subscription-account__credits--urgent {
  border-color: color-mix(in srgb, var(--color-action) 40%, var(--color-border-subtle));
  background: var(--color-action-soft);
}
.subscription-account__credits > span:first-child {
  color: var(--color-text-faint);
}
.subscription-account__credits-expiry {
  color: var(--color-text-faint);
}
.subscription-account__credits-dots {
  display: inline-flex;
  gap: 3px;
}
.subscription-account__credits-dots i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--color-action);
}
.subscription-account__alert {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-3);
  border: 1px solid var(--color-feedback-danger-border);
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  padding: 9px 12px;
  color: var(--color-text);
  font-size: var(--text-meta);
}
.subscription-account__alert > span {
  min-width: 0;
  flex: 1 1 auto;
}
.subscription-account__menu {
  display: grid;
  width: 100%;
  gap: 1px;
}
.subscription-account__menu button {
  display: flex;
  width: 100%;
  align-items: center;
  gap: var(--space-2);
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text);
  padding: 7px 6px;
  font: inherit;
  font-size: var(--text-button);
  text-align: left;
  cursor: pointer;
}
.subscription-account__menu button svg {
  flex: none;
  color: var(--color-text-faint);
}
.subscription-account__menu button:hover:not(:disabled) {
  background: var(--color-surface-sunken);
}
.subscription-account__menu button:hover:not(:disabled) svg {
  color: var(--color-text-muted);
}
.subscription-account__menu button:disabled {
  cursor: not-allowed;
  opacity: 0.46;
}
.subscription-account__menu-divider {
  height: 1px;
  margin: 4px -8px;
  background: var(--color-border-subtle);
}
.subscription-account__menu button.subscription-account__menu-danger,
.subscription-account__menu button.subscription-account__menu-danger svg {
  color: var(--color-danger);
}
.subscription-account__menu button.subscription-account__menu-danger:hover:not(:disabled) {
  background: var(--color-danger-bg);
}
/* 左右是时间信息，中间保留最重要的近 24 小时结果；窄屏再换行避免压缩数字。 */
.subscription-account__meta {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  align-items: baseline;
  gap: var(--space-2) var(--space-4);
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.subscription-account__meta > span {
  min-width: 0;
  white-space: nowrap;
}
.subscription-account__meta-window {
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  padding: 1px 6px;
  font-size: var(--text-label-xs);
}
.subscription-account__meta-last-used {
  justify-self: start;
}
.subscription-account__meta-daily {
  display: inline-flex;
  justify-self: center;
  align-items: baseline;
  gap: var(--space-3);
}
.subscription-account__daily-outcomes,
.subscription-account__daily-outcomes > span {
  display: inline-flex;
  align-items: baseline;
  gap: var(--space-1);
}
.subscription-account__daily-outcomes {
  color: var(--color-text-faint);
}
.subscription-account__daily-outcomes strong {
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 620;
  font-variant-numeric: tabular-nums;
}
.subscription-account__daily-success {
  color: var(--color-success);
}
.subscription-account__daily-failure {
  color: var(--color-danger);
}
.subscription-account__daily-outcomes > .subscription-account__daily-separator {
  color: var(--color-text-faint);
}
.subscription-account__meta-sync {
  grid-column: 3;
  justify-self: end;
}
.subscription-account__diagnostics-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: var(--space-2) var(--space-4);
  margin: var(--space-2) 0 0;
}
.subscription-account__diagnostics-grid dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-account__diagnostics-grid dd {
  margin: 2px 0 0;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-variant-numeric: tabular-nums;
}
.subscription-account__reset-credit-list {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  margin-top: var(--space-2);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-account__models {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-3);
  font-size: var(--text-sm);
}
.subscription-account__models span {
  color: var(--color-text-faint);
}
.subscription-account__models code {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 3px 7px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}
@media (max-width: 640px) {
  .subscription-account__quota {
    grid-template-columns: minmax(80px, 104px) minmax(0, 1fr) max-content;
  }
  .subscription-account__quota-reset {
    grid-column: 1 / -1;
    text-align: left;
  }
  .subscription-account__meta {
    grid-template-columns: minmax(0, 1fr) max-content;
  }
  .subscription-account__meta-daily {
    grid-column: 1 / -1;
    grid-row: 1;
    justify-self: center;
  }
  .subscription-account__meta-last-used {
    grid-column: 1;
    grid-row: 2;
    justify-self: start;
  }
  .subscription-account__meta-sync {
    grid-column: 2;
    grid-row: 2;
  }
  .subscription-account__top :deep(.app-button),
  .subscription-account__top :deep(.icon-button) {
    min-height: var(--touch-target);
  }
}
</style>

<style>
/* PopoverContent 由 AppPopover 渲染，作用域样式够不到它，按既有的
   app-popover__content--<name> 约定做全局修饰（与 PreferencesControl 一致）。
   默认 360px 宽对一个三四项的菜单来说是一大片空白。 */
.app-popover__content.app-popover__content--account-menu {
  width: auto;
  min-width: 180px;
  border-color: var(--color-border-control);
  border-radius: 10px;
  padding: 8px;
}
</style>
