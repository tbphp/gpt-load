<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  CredentialItemDto,
  CredentialQuotaLabelKey,
  CredentialQuotaWindowDto,
} from '@/api/control/types'
import type { HomeSubscriptionAccountDto } from '@/app/resources/home'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatLocalInstant } from '@/lib/format'
import { quotaProgressTone, type QuotaProgressTone } from '@/lib/quota-progress'

const props = defineProps<{ account: HomeSubscriptionAccountDto }>()

const { locale, n, t, te } = useI18n()

type UnifiedStatus =
  | 'available'
  | 'quota_exhausted'
  | 'cooldown'
  | 'blacklisted'
  | 'refreshing'
  | 'needs_reauth'
  | 'outcome_unknown'
  | 'disabled'
type CardTone = 'success' | 'warning' | 'danger' | 'neutral'
type ResetCreditDotTone = 'default' | 'warning' | 'danger'

// 与分组详情页 SubscriptionAccountCard 同步：重置卡到期档位用真实时钟而非一次性
// Date.now()，避免首页长时间停留在前台时点阵颜色停留在打开时刻。
const nowMs = ref(Date.now())
let clockTimer: number | undefined

onMounted(() => {
  clockTimer = window.setInterval(() => {
    nowMs.value = Date.now()
  }, 30_000)
})
onBeforeUnmount(() => {
  if (clockTimer !== undefined) window.clearInterval(clockTimer)
})

const credential = computed<CredentialItemDto>(() => props.account.credential)
const snapshot = computed(() => credential.value.observation?.snapshot)
const accountName = computed(() => credential.value.account.email ?? credential.value.mask)
const planLabel = computed(() => snapshot.value?.plan_summary.name?.trim() ?? '')
const channelTooltip = computed(() =>
  [props.account.channel_name, planLabel.value].filter(Boolean).join(' · '),
)

function isAccountWideQuotaWindow(window: CredentialQuotaWindowDto): boolean {
  return window.scope === 'account'
}

function quotaWindowDuration(window: CredentialQuotaWindowDto): number {
  const seconds = window.window_seconds
  return seconds !== undefined && Number.isFinite(seconds) && seconds > 0
    ? seconds
    : Number.MAX_SAFE_INTEGER
}

// 按 scope + 周期排序全部窗口；焦点数字与状态判断都必须看完整列表，
// 只有底部分段带需要限制展示数量（见下方 quotaWindows）。
const sortedQuotaWindows = computed(() =>
  [...(snapshot.value?.quota_windows ?? [])].sort((left, right) => {
    const scopeDifference =
      Number(!isAccountWideQuotaWindow(left)) - Number(!isAccountWideQuotaWindow(right))
    return scopeDifference !== 0
      ? scopeDifference
      : quotaWindowDuration(left) - quotaWindowDuration(right)
  }),
)

// 分段带最多展示 4 条；若焦点窗口（见下方 lead）因排序被挤到第 5 位之后，
// 仍强制并入前排，保证大号数字与分段带指向同一个窗口，而不是各说各话。
const quotaWindows = computed(() => {
  const capped = sortedQuotaWindows.value.slice(0, 4)
  const leadWindow = lead.value?.window
  if (leadWindow && !capped.some((window) => window.id === leadWindow.id)) {
    return [leadWindow, ...capped.slice(0, 3)]
  }
  return capped
})

const unifiedStatus = computed<UnifiedStatus>(() => {
  const item = credential.value
  if (item.configured_status === 'disabled') return 'disabled'
  if (item.auth_state === 'refreshing') return 'refreshing'
  if (item.auth_state === 'reauthorization_required') return 'needs_reauth'
  if (item.auth_state === 'outcome_unknown') return 'outcome_unknown'
  if (item.effective_status === 'blacklisted') return 'blacklisted'
  if (item.effective_status === 'cooldown') return 'cooldown'
  if (
    props.account.capabilities.quota_observation &&
    sortedQuotaWindows.value.some(
      (window) => window.scope === 'account' && window.state === 'exhausted',
    )
  ) {
    return 'quota_exhausted'
  }
  return 'available'
})

// 同一账号可能被多个分组共用；分组数 > 1 时可用性以「当前有几个分组能调度到它」为准。
// 分组详情页 SubscriptionAccountCard 原本也有同名聚合口径，但那条路径只在 readonly
// 模式下触发，首页改用本组件后已成为死代码并被清理，这里是唯一仍生效的实现。
const showAggregateAvailability = computed(
  () =>
    props.account.group_count > 1 &&
    (props.account.available_group_count < props.account.group_count ||
      unifiedStatus.value === 'available'),
)

const cardTone = computed<CardTone>(() => {
  if (showAggregateAvailability.value) {
    if (props.account.available_group_count === props.account.group_count) return 'success'
    return props.account.available_group_count > 0 ? 'warning' : 'danger'
  }
  const tones: Record<UnifiedStatus, CardTone> = {
    available: 'success',
    quota_exhausted: 'warning',
    cooldown: 'warning',
    blacklisted: 'danger',
    refreshing: 'neutral',
    needs_reauth: 'danger',
    outcome_unknown: 'danger',
    disabled: 'neutral',
  }
  return tones[unifiedStatus.value]
})

const statusLabel = computed(() =>
  showAggregateAvailability.value
    ? t('home.ledger.subscriptionAccounts.availableGroups', {
        available: n(props.account.available_group_count),
        total: n(props.account.group_count),
      })
    : t(`group.credentials.subscription.status.${unifiedStatus.value}`),
)

const displayDisabled = computed(
  () => !showAggregateAvailability.value && unifiedStatus.value === 'disabled',
)

// 单分组且状态正常时不显示状态角标：首页只在有异常，或聚合可用性值得一提时才发声，
// 呼应 HomeAttention「没有要处理的事就整块不渲染」的克制原则。
const showStatusChip = computed(
  () => showAggregateAvailability.value || unifiedStatus.value !== 'available',
)

function remainingPercent(window: CredentialQuotaWindowDto): number | undefined {
  if (window.utilization !== undefined) return Math.round((1 - window.utilization) * 100)
  if (window.remaining !== undefined && window.limit && window.limit > 0) {
    return Math.max(0, Math.min(100, Math.round((window.remaining / window.limit) * 100)))
  }
  return undefined
}

function quotaTone(window: CredentialQuotaWindowDto): QuotaProgressTone | undefined {
  const value = remainingPercent(window)
  return value === undefined ? undefined : quotaProgressTone(value, window.state === 'exhausted')
}

function quotaFillStyle(window: CredentialQuotaWindowDto): Record<string, string> {
  const value = remainingPercent(window)
  return value === undefined ? {} : { width: `${value}%` }
}

// 与分组详情页 quotaWindowPeriodLabel 完全一致：日/时之外还兜底分钟与秒，
// 避免非整日整时的窗口（例如 30 分钟）拿不到任何周期文案。
function quotaWindowPeriodLabel(seconds: number | undefined): string {
  if (seconds === undefined || !Number.isSafeInteger(seconds) || seconds <= 0) return ''
  const day = 24 * 60 * 60
  const hour = 60 * 60
  const minute = 60
  if (seconds % day === 0) return `${seconds / day}d`
  if (seconds % hour === 0) return `${seconds / hour}h`
  if (seconds % minute === 0) return `${seconds / minute}min`
  return `${seconds}s`
}

const quotaSubjectKeys: Readonly<Record<string, CredentialQuotaLabelKey>> = {
  session: 'session',
  weekly: 'weekly',
  'extra usage': 'extra_usage',
  'included usage': 'included_usage',
  'pay as you go': 'pay_as_you_go',
  'oauth apps': 'oauth_apps',
}

function normalizedQuotaLabelPart(value: string): string {
  return value.trim().toLowerCase().replaceAll('_', ' ').replaceAll('-', ' ')
}

function translatedQuotaLabel(labelKey: CredentialQuotaLabelKey, fallback: string): string {
  const key = `group.credentials.subscription.quotaLabels.${labelKey}`
  return te(key) ? t(key) : fallback
}

// 与分组详情页 quotaWindowLabel 完全一致：label_key 为 oauth_apps 时保留周期后缀
// （例如「OAuth 应用 · 7d」），否则不同周期的同类窗口会显示成完全相同的文案。
function quotaWindowLabel(window: CredentialQuotaWindowDto): string {
  const period = quotaWindowPeriodLabel(window.window_seconds)
  if (period && window.scope === 'account') return period

  if (window.label_key) {
    const subject = translatedQuotaLabel(window.label_key, window.label)
    return window.label_key === 'oauth_apps' && period ? `${subject} · ${period}` : subject
  }

  const parts = window.label
    .split('·')
    .map((part) => part.trim())
    .filter(Boolean)
  if (parts.length === 0) return period

  const firstPart = normalizedQuotaLabelPart(parts[0] ?? '')
  if (period && (firstPart === 'session' || firstPart === 'weekly')) return period

  return parts
    .map((part) => {
      const labelKey = quotaSubjectKeys[normalizedQuotaLabelPart(part)]
      return labelKey ? translatedQuotaLabel(labelKey, part) : part
    })
    .join(' · ')
}

function quotaValueLabel(window: CredentialQuotaWindowDto): string {
  const value = remainingPercent(window)
  if (value !== undefined) {
    return t('group.credentials.subscription.remainingPercent', { value: n(value) })
  }
  if (window.remaining !== undefined && window.limit !== undefined) {
    return t('group.credentials.subscription.remaining', {
      remaining: n(window.remaining),
      limit: n(window.limit),
    })
  }
  if (window.remaining !== undefined) {
    return t('group.credentials.subscription.remainingAmount', { remaining: n(window.remaining) })
  }
  return t('group.credentials.subscription.unknown')
}

interface QuotaWindowPeriod {
  startMS: number
  endMS: number
}

function quotaWindowPeriod(window: CredentialQuotaWindowDto): QuotaWindowPeriod | undefined {
  const endMS = window.reset_at_ms
  const seconds = window.window_seconds
  if (
    endMS === undefined ||
    seconds === undefined ||
    !Number.isSafeInteger(endMS) ||
    !Number.isSafeInteger(seconds) ||
    endMS <= 0 ||
    seconds <= 0
  ) {
    return undefined
  }
  const durationMS = seconds * 1_000
  if (!Number.isSafeInteger(durationMS) || durationMS > endMS) return undefined
  return { startMS: endMS - durationMS, endMS }
}

function quotaWindowNeedsRefresh(window: CredentialQuotaWindowDto): boolean {
  return window.reset_at_ms !== undefined && window.reset_at_ms <= nowMs.value
}

// 与分组详情页 quotaPeriodTooltip 完全一致：给出周期起止，周期已结束但数据未刷新时
// 换成「待刷新」提示——而不是一句可能已经过期的相对时间。
function quotaPeriodTooltip(window: CredentialQuotaWindowDto): string | undefined {
  const resetAtMS = window.reset_at_ms
  if (resetAtMS === undefined) return undefined
  const resetAt = formatLocalInstant(resetAtMS, locale.value)
  const period = quotaWindowPeriod(window)
  const periodLabel = period
    ? t('group.credentials.subscription.quotaPeriod', {
        start: formatLocalInstant(period.startMS, locale.value),
        end: resetAt,
      })
    : resetAt
  return quotaWindowNeedsRefresh(window)
    ? t('group.credentials.subscription.quotaPendingHint', { period: periodLabel })
    : periodLabel
}

function quotaTooltip(window: CredentialQuotaWindowDto): string {
  return [quotaWindowLabel(window), quotaValueLabel(window), quotaPeriodTooltip(window)]
    .filter((line): line is string => Boolean(line))
    .join('\n')
}

const quotaResetPrefix = computed(() => t('group.credentials.subscription.quotaResetPrefix'))
const quotaResetSuffix = computed(() => t('group.credentials.subscription.quotaResetSuffix'))

// 焦点窗口：额度百分比已知的窗口里剩余最少的那个，在全部窗口（而非展示截断后
// 的 quotaWindows）中查找，避免真正最紧张的窗口因排序被截断而漏判。上游只给出
// state、没有具体数值的窗口（Codex/Claude 都存在）仍要退回展示，只是百分比留空，
// 不能因为找不到数值就当成完全没有额度窗口。
const lead = computed<
  { window: CredentialQuotaWindowDto; percent: number | undefined } | undefined
>(() => {
  let best: { window: CredentialQuotaWindowDto; percent: number } | undefined
  for (const window of sortedQuotaWindows.value) {
    const percent = remainingPercent(window)
    if (percent === undefined) continue
    if (best === undefined || percent < best.percent) best = { window, percent }
  }
  if (best) return best
  const [first] = sortedQuotaWindows.value
  return first ? { window: first, percent: undefined } : undefined
})
const leadTone = computed(() => (lead.value ? quotaTone(lead.value.window) : undefined))

const resetCreditsAvailable = computed(() => snapshot.value?.reset_credits_available ?? 0)
const resetCredits = computed(() => snapshot.value?.reset_credits ?? [])
const availableResetCreditDetails = computed(() =>
  resetCredits.value.slice(0, resetCreditsAvailable.value),
)
const hasResetCredits = computed(
  () =>
    props.account.capabilities.credential_actions.includes('reset_credit') &&
    resetCreditsAvailable.value > 0,
)

// 点阵最多画 5 个，避免账号数多、卡片窄时被点阵撑爆；确切张数留给 tooltip 与读屏文本。
const resetCreditDots = computed(() =>
  Array.from({ length: Math.min(resetCreditsAvailable.value, 5) }, (_, index) => {
    const expiresAtMS = availableResetCreditDetails.value[index]?.expires_at_ms
    let tone: ResetCreditDotTone = 'default'
    if (expiresAtMS !== undefined) {
      const remainingMS = expiresAtMS - nowMs.value
      tone =
        remainingMS <= 24 * 60 * 60 * 1_000
          ? 'danger'
          : remainingMS <= 48 * 60 * 60 * 1_000
            ? 'warning'
            : 'default'
    }
    return { index, tone }
  }),
)

function resetCreditExpiryLabel(credit: (typeof resetCredits.value)[number]): string {
  if (credit.expires_at_ms === undefined) {
    return t('group.credentials.subscription.resetCreditPermanent')
  }
  if (credit.expires_at_ms <= nowMs.value) {
    return t('group.credentials.subscription.resetCreditExpired')
  }
  return formatLocalInstant(credit.expires_at_ms, locale.value)
}

// 与分组详情页一致：一枚 tooltip 覆盖全部重置卡（标题 + 逐张到期时间），而不是逐点悬浮。
const resetCreditsTooltip = computed(() => {
  const lines = availableResetCreditDetails.value.length
    ? availableResetCreditDetails.value.map((credit, index) =>
        t('group.credentials.subscription.resetCreditsTooltipItem', {
          index: index + 1,
          expires: resetCreditExpiryLabel(credit),
        }),
      )
    : [t('group.credentials.subscription.resetCreditsTooltipNoDetails')]
  const knownCount = availableResetCreditDetails.value.length
  if (knownCount < resetCreditsAvailable.value) {
    lines.push(
      t('group.credentials.subscription.resetCreditsTooltipMore', {
        count: n(resetCreditsAvailable.value - knownCount),
      }),
    )
  }
  return [t('group.credentials.subscription.resetCreditsTooltipTitle'), ...lines].join('\n')
})
</script>

<template>
  <article
    class="home-subscription-mini"
    :class="{ 'home-subscription-mini--off': unifiedStatus === 'disabled' }"
    :aria-label="`${accountName} · ${statusLabel}`"
  >
    <span class="sr-only">{{ statusLabel }}</span>

    <div class="home-subscription-mini__top">
      <span class="home-subscription-mini__channel" role="img" :aria-label="channelTooltip">
        <ChannelIcon :icon="account.channel_icon" :mark="account.channel_mark" />
      </span>
      <OverflowTooltip class="home-subscription-mini__account" :content="accountName">
        {{ accountName }}
      </OverflowTooltip>
      <span v-if="planLabel" class="home-subscription-mini__plan">{{ planLabel }}</span>
    </div>

    <div class="home-subscription-mini__lead">
      <StatusBadge
        v-if="showStatusChip"
        class="home-subscription-mini__status"
        :tone="cardTone"
        :icon="displayDisabled ? 'off' : undefined"
        size="compact"
      >
        {{ statusLabel }}
      </StatusBadge>
      <span
        v-else-if="lead && lead.percent !== undefined"
        class="home-subscription-mini__num"
        :class="`home-subscription-mini__num--${leadTone ?? 'unknown'}`"
      >
        <strong>{{ n(lead.percent) }}</strong
        ><span aria-hidden="true">%</span>
      </span>
      <span v-else class="home-subscription-mini__num home-subscription-mini__num--empty">—</span>

      <span class="home-subscription-mini__meta">
        <template v-if="lead">
          {{ quotaWindowLabel(lead.window) }}
          <template v-if="lead.window.reset_at_ms">
            ·
            <AppTooltip
              v-if="quotaWindowNeedsRefresh(lead.window)"
              :content="quotaPeriodTooltip(lead.window) ?? ''"
            >
              <span class="home-subscription-mini__pending" tabindex="0">{{
                t('group.credentials.subscription.quotaPendingRefresh')
              }}</span>
            </AppTooltip>
            <template v-else>
              <span v-if="quotaResetPrefix">{{ quotaResetPrefix }}</span>
              <AppRelativeTime
                :instant="lead.window.reset_at_ms"
                :locale="locale"
                :empty-label="t('group.credentials.subscription.unknown')"
                :tooltip-content="quotaPeriodTooltip(lead.window)"
                hint
              /><span v-if="quotaResetSuffix">{{ quotaResetSuffix }}</span>
            </template>
          </template>
        </template>
        <template v-else>{{ t('group.credentials.subscription.noQuota') }}</template>
      </span>
    </div>

    <div
      v-if="quotaWindows.length > 1 || hasResetCredits"
      class="home-subscription-mini__resources"
    >
      <div
        v-if="quotaWindows.length > 1"
        class="home-subscription-mini__strip"
        role="group"
        :aria-label="t('group.credentials.subscription.title')"
      >
        <AppTooltip v-for="window in quotaWindows" :key="window.id" :content="quotaTooltip(window)">
          <span
            class="home-subscription-mini__seg"
            :class="`home-subscription-mini__seg--${quotaTone(window) ?? 'unknown'}`"
            :role="remainingPercent(window) === undefined ? 'img' : 'progressbar'"
            :aria-label="quotaTooltip(window)"
            :aria-valuemin="remainingPercent(window) === undefined ? undefined : 0"
            :aria-valuemax="remainingPercent(window) === undefined ? undefined : 100"
            :aria-valuenow="remainingPercent(window)"
            tabindex="0"
          >
            <span
              v-if="remainingPercent(window) !== undefined"
              class="home-subscription-mini__seg-fill"
              :style="quotaFillStyle(window)"
              aria-hidden="true"
            ></span>
          </span>
        </AppTooltip>
      </div>
      <span v-else class="home-subscription-mini__spacer" aria-hidden="true"></span>

      <AppTooltip v-if="hasResetCredits" :content="resetCreditsTooltip">
        <span
          class="home-subscription-mini__credits"
          tabindex="0"
          :aria-label="resetCreditsTooltip"
        >
          <i
            v-for="dot in resetCreditDots"
            :key="dot.index"
            class="home-subscription-mini__credit-dot"
            :class="`home-subscription-mini__credit-dot--${dot.tone}`"
          ></i>
          <span
            v-if="resetCreditsAvailable > resetCreditDots.length"
            class="home-subscription-mini__credit-more"
            aria-hidden="true"
            >+</span
          >
        </span>
      </AppTooltip>
    </div>
  </article>
</template>

<style scoped>
.home-subscription-mini {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 8px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  box-shadow: var(--shadow-card);
  padding: 10px 11px;
}

.home-subscription-mini--off {
  background: var(--color-surface-sunken);
}

.home-subscription-mini--off .home-subscription-mini__account,
.home-subscription-mini--off .home-subscription-mini__meta {
  color: var(--color-text-faint);
}

.home-subscription-mini__top {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
}

.home-subscription-mini__channel {
  display: inline-flex;
  width: 16px;
  height: 16px;
  flex: none;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
}

.home-subscription-mini__seg:focus-visible,
.home-subscription-mini__credits:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.home-subscription-mini__channel :deep(.channel-icon) {
  width: 16px;
  height: 16px;
  font-size: 16px;
}

.home-subscription-mini__account {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 600;
  line-height: 16px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-subscription-mini__plan {
  flex: none;
  border-radius: 5px;
  background: var(--color-tag);
  padding: 1px 6px;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 600;
  white-space: nowrap;
}

.home-subscription-mini__lead {
  display: flex;
  align-items: flex-end;
  gap: 7px;
}

.home-subscription-mini__num {
  display: inline-flex;
  flex: none;
  align-items: baseline;
  gap: 1px;
  font-variant-numeric: tabular-nums;
}

.home-subscription-mini__num strong {
  font-size: 25px;
  font-weight: 640;
  letter-spacing: -0.02em;
  line-height: 1;
}

.home-subscription-mini__num span {
  font-size: 12px;
  font-weight: 600;
}

/* 与分组详情页 SubscriptionAccountCard 的配额条同一套强调色（oklch，单值适配明暗），
   而非通用状态语义色——这三个数值代表的是额度剩余量，不是运行状态。 */
.home-subscription-mini__num--success {
  color: oklch(70% 0.16 158);
}

.home-subscription-mini__num--warning {
  color: oklch(75% 0.152 75);
}

.home-subscription-mini__num--danger {
  color: oklch(65% 0.2 22);
}

.home-subscription-mini__num--unknown {
  color: var(--color-text-faint);
}

.home-subscription-mini__num--empty {
  color: var(--color-text-faint);
  font-size: 21px;
  font-weight: 500;
  line-height: 1;
}

.home-subscription-mini__meta {
  display: block;
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  padding-bottom: 3px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.3;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-subscription-mini__meta :deep(.app-relative-time--hint) {
  text-decoration-color: var(--color-border-control);
}

.home-subscription-mini__pending {
  cursor: help;
  text-decoration: underline dotted;
  text-decoration-color: var(--color-border-control);
  text-underline-offset: 3px;
}

.home-subscription-mini__pending:focus-visible {
  border-radius: 3px;
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.home-subscription-mini__resources {
  display: flex;
  align-items: center;
  gap: 8px;
  row-gap: 6px;
  flex-wrap: wrap;
}

.home-subscription-mini__status {
  flex: none;
}

.home-subscription-mini__strip {
  display: flex;
  min-width: 32px;
  flex: 1 1 32px;
  align-items: center;
  gap: 4px;
}

.home-subscription-mini__spacer {
  flex: 1 1 auto;
}

.home-subscription-mini__seg {
  position: relative;
  display: block;
  flex: 1 1 0;
  height: 4px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--seg-track, var(--color-surface-sunken));
  cursor: help;
}

/* 与分组详情页 SubscriptionAccountCard 的配额条同一套颜色值（quota--success/warning/
   danger），保持首页摘要与分组详情页对同一份额度数据的视觉表达一致。 */
.home-subscription-mini__seg--success {
  --seg-track: light-dark(#dcfeea, #112b21);
  --seg-fill: oklch(70% 0.16 158);
}

.home-subscription-mini__seg--warning {
  --seg-track: light-dark(#fff2e2, #302212);
  --seg-fill: oklch(75% 0.152 75);
}

.home-subscription-mini__seg--danger {
  --seg-track: light-dark(#fef0f0, #371a1d);
  --seg-fill: oklch(65% 0.2 22);
}

.home-subscription-mini__seg-fill {
  position: absolute;
  inset: 0 auto 0 0;
  border-radius: inherit;
  background: var(--seg-fill, var(--color-border-control));
  transition: width var(--duration-fast) var(--easing-standard);
}

.home-subscription-mini__credits {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: 3px;
  cursor: help;
}

.home-subscription-mini__credit-dot {
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: 50%;
  background: var(--color-action);
}

.home-subscription-mini__credit-dot--warning {
  background: var(--color-warning);
}

.home-subscription-mini__credit-dot--danger {
  background: var(--color-danger);
}

.home-subscription-mini__credit-more {
  color: var(--color-text-faint);
  font-size: 9px;
  font-weight: 700;
  line-height: 1;
}

@media (prefers-reduced-motion: reduce) {
  .home-subscription-mini__seg-fill {
    transition: none;
  }
}
</style>
