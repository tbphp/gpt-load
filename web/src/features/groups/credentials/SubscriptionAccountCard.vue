<script setup lang="ts">
import {
  CircleCheck,
  CircleOff,
  Ellipsis,
  LoaderCircle,
  LogIn,
  RotateCcw,
  Trash2,
} from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialItemDto, CredentialQuotaWindowDto } from '@/api/control/types'
import type { ChannelAuthorizationMethod, ChannelCapabilitiesDto } from '@/app/resources/channels'
import AppButton from '@/components/ui/AppButton.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatEstimatedCost, formatLocalInstant, formatTokens } from '@/lib/format'

import { presentCredentialFailureCategory } from './credential-failure-presenter'

const props = defineProps<{
  item: CredentialItemDto
  busy: boolean
  detailBusy: boolean
  detailLoaded: boolean
  detailError: string
  authorizationMethods: ChannelAuthorizationMethod[]
  capabilities: ChannelCapabilitiesDto
}>()
const emit = defineEmits<{
  toggle: [item: CredentialItemDto]
  restore: [item: CredentialItemDto]
  refresh: [item: CredentialItemDto]
  'load-details': [item: CredentialItemDto]
  reset: [item: CredentialItemDto]
  reauthorize: [item: CredentialItemDto]
  remove: [item: CredentialItemDto]
}>()
const { locale, n, t } = useI18n()
const menuOpen = ref(false)
const detailsExpanded = ref(false)
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

watch(
  () =>
    [
      detailsExpanded.value,
      props.detailLoaded,
      props.detailBusy,
      props.detailError,
      props.busy,
    ] as const,
  ([expanded, loaded, detailBusy, error, busy]) => {
    if (expanded && !loaded && !detailBusy && !error && !busy) {
      emit('load-details', props.item)
    }
  },
  { flush: 'post' },
)

const observation = computed(() => props.item.observation)
const supportsQuotaObservation = computed(() => props.capabilities.quota_observation)
const supportsResetCredit = computed(() =>
  props.capabilities.credential_actions.includes('reset_credit'),
)
const canReauthorize = computed(() => props.authorizationMethods.length > 0)
const snapshot = computed(() => observation.value?.snapshot)
// 最快恢复的额度窗口排在前面；缺少时长的上游窗口保持在最后。
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
const windowSkeletonHeight = computed(() => {
  const rows = accountQuotaWindows.value.length
  return rows === 0 ? '32px' : `${24 + rows * 32}px`
})
const constrainedModels = computed(() =>
  Array.from(new Set(quotaWindows.value.flatMap((window) => window.model_ids ?? []))),
)
const accountName = computed(() => props.item.account.email ?? props.item.mask)
const planLabel = computed(() => {
  const plan = snapshot.value?.plan_summary.name?.trim()
  if (!plan) return ''
  const normalized = plan.toLowerCase().replaceAll('_', '-').replaceAll(' ', '')
  const labels: Readonly<Record<string, string>> = {
    pro: 'Pro 20x',
    prolite: 'Pro 5x',
    'pro-lite': 'Pro 5x',
    plus: 'Plus',
    team: 'Team',
    free: 'Free',
  }
  return labels[normalized] ?? plan
})
const credentialExpiryTooltip = computed(() => {
  const expiresAtMS = props.item.account.expires_at_ms
  if (expiresAtMS === undefined) return undefined
  const exact = formatLocalInstant(expiresAtMS, locale.value)
  return props.item.auth_state === 'ready'
    ? `${exact}\n${t('group.credentials.subscription.autoRenews')}`
    : exact
})
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
    supportsQuotaObservation.value &&
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
const dailyUsage = computed(() => props.item.daily_usage)
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

function estimateTitles(...keys: string[]): string | undefined {
  const titles = keys.filter(Boolean).map((key) => t(key))
  return titles.length === 0 ? undefined : titles.join(' · ')
}

function requestCountTitle(window: CredentialQuotaWindowDto): string | undefined {
  return window.observed_usage?.data_complete === false
    ? t('group.credentials.subscription.estimate.dataIncomplete')
    : undefined
}

function tokenCountTitle(window: CredentialQuotaWindowDto): string | undefined {
  const observed = window.observed_usage
  if (!observed) return undefined
  return estimateTitles(
    observed.data_complete ? '' : 'group.credentials.subscription.estimate.dataIncomplete',
    observed.usage_complete ? '' : 'group.credentials.subscription.estimate.usageIncomplete',
  )
}

function referenceCostTitle(window: CredentialQuotaWindowDto): string | undefined {
  const observed = window.observed_usage
  if (!observed) return undefined
  return estimateTitles(
    observed.data_complete ? '' : 'group.credentials.subscription.estimate.dataIncomplete',
    observed.pricing_complete ? '' : 'group.credentials.subscription.estimate.pricingIncomplete',
  )
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
  if (value !== undefined) {
    return t('group.credentials.subscription.remainingPercent', { value: n(value) })
  }
  if (window.remaining !== undefined && window.limit !== undefined) {
    return t('group.credentials.subscription.remaining', {
      remaining: n(window.remaining),
      limit: n(window.limit),
    })
  }
  return t('group.credentials.subscription.unknown')
}

function quotaPeriodTooltip(window: CredentialQuotaWindowDto): string | undefined {
  const resetAtMS = window.reset_at_ms
  if (resetAtMS === undefined) return undefined
  const resetAt = formatLocalInstant(resetAtMS, locale.value)
  const windowSeconds = window.window_seconds
  if (windowSeconds === undefined || windowSeconds <= 0) return resetAt
  const windowMS = windowSeconds * 1_000
  if (!Number.isSafeInteger(windowMS) || windowMS > resetAtMS) return resetAt
  return t('group.credentials.subscription.quotaPeriod', {
    start: formatLocalInstant(resetAtMS - windowMS, locale.value),
    end: resetAt,
  })
}

function usedPercentValue(window: CredentialQuotaWindowDto): string {
  const remaining = remainingPercent(window)
  return remaining === undefined ? '—' : `${n(100 - remaining)}%`
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

function toggleDetails(): void {
  if (props.detailBusy) return
  detailsExpanded.value = !detailsExpanded.value
}

function retryDetails(): void {
  emit('load-details', props.item)
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
    <div class="subscription-account__main">
      <header class="subscription-account__top">
        <OverflowTooltip class="subscription-account__mail" :content="accountName">
          {{ accountName }}
        </OverflowTooltip>
        <span v-if="planLabel" class="subscription-account__plan">{{ planLabel }}</span>
        <StatusBadge :tone="statusTone" size="compact">
          {{ t(`group.credentials.subscription.status.${unifiedStatus}`) }}
        </StatusBadge>
        <div class="subscription-account__actions">
          <span v-if="supportsQuotaObservation" class="subscription-account__sync-age">
            {{ t('group.credentials.subscription.synced') }}
            <AppRelativeTime
              :instant="observation?.observed_at_ms ?? null"
              :locale="locale"
              :empty-label="t('group.credentials.subscription.unknown')"
              hint
            />
          </span>
          <AppButton
            v-if="supportsQuotaObservation"
            class="subscription-account__sync-button"
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
              <button
                v-if="canReauthorize"
                type="button"
                :disabled="busy"
                @click="runMenuAction('reauthorize')"
              >
                <LogIn :size="15" aria-hidden="true" />{{
                  t('group.credentials.subscription.reauthorize')
                }}
              </button>
              <button type="button" :disabled="busy" @click="runMenuAction('toggle')">
                <CircleOff
                  v-if="item.configured_status === 'active'"
                  :size="15"
                  aria-hidden="true"
                />
                <CircleCheck v-else :size="15" aria-hidden="true" />
                {{
                  item.configured_status === 'active'
                    ? t('group.credentials.disable')
                    : t('group.credentials.enable')
                }}
              </button>
              <button
                v-if="isProblem"
                type="button"
                :disabled="busy"
                @click="runMenuAction('restore')"
              >
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
      </header>

      <div v-if="authIssue" class="subscription-account__alert">
        <span>{{ authIssue }}</span>
        <AppButton
          v-if="canReauthorize"
          size="compact"
          :disabled="busy"
          @click="emit('reauthorize', item)"
        >
          {{ t('group.credentials.subscription.reauthorize') }}
        </AppButton>
      </div>

      <div
        v-if="supportsQuotaObservation && quotaWindows.length"
        class="subscription-account__quotas"
      >
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
            <AppRelativeTime
              v-if="window.reset_at_ms"
              :instant="window.reset_at_ms"
              :locale="locale"
              :empty-label="t('group.credentials.subscription.unknown')"
              :tooltip-content="quotaPeriodTooltip(window)"
              hint
            />
            <template v-else>—</template>
          </span>
        </div>
      </div>
      <p v-else-if="supportsQuotaObservation" class="subscription-account__faint">
        {{ t('group.credentials.subscription.noQuota') }}
      </p>

      <p v-if="unifiedStatus === 'quota_exhausted'" class="subscription-account__hint">
        {{ t('group.credentials.subscription.quotaExhaustedHint') }}
      </p>

      <div
        v-if="supportsResetCredit && resetCreditsUsable"
        class="subscription-account__credits"
        :class="{ 'subscription-account__credits--urgent': unifiedStatus === 'quota_exhausted' }"
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

      <div class="subscription-account__detail-control">
        <AppTooltip
          :content="
            t(
              detailsExpanded
                ? 'group.credentials.subscription.collapseDetails'
                : 'group.credentials.subscription.expandDetails',
            )
          "
        >
          <button
            class="subscription-account__detail-toggle"
            type="button"
            :disabled="detailBusy"
            :aria-expanded="detailsExpanded"
            :aria-controls="`credential-detail-${item.credential_id}`"
            :aria-label="
              t(
                detailsExpanded
                  ? 'group.credentials.subscription.collapseDetails'
                  : 'group.credentials.subscription.expandDetails',
              )
            "
            :aria-busy="detailBusy ? 'true' : undefined"
            @click="toggleDetails"
          >
            <span class="subscription-account__detail-disc">
              <LoaderCircle
                v-if="detailBusy"
                class="subscription-account__detail-spinner"
                :size="15"
                aria-hidden="true"
              />
              <svg
                v-else
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
                aria-hidden="true"
              >
                <path
                  d="M5 4.5h14A1.5 1.5 0 0 1 20.5 6v12a1.5 1.5 0 0 1-1.5 1.5H5A1.5 1.5 0 0 1 3.5 18V6A1.5 1.5 0 0 1 5 4.5Z"
                />
                <path d="M3.5 10h17" />
                <path :d="detailsExpanded ? 'm9 17 3-3 3 3' : 'm9 14 3 3 3-3'" />
              </svg>
            </span>
          </button>
        </AppTooltip>
      </div>
    </div>

    <section
      v-if="detailsExpanded"
      :id="`credential-detail-${item.credential_id}`"
      class="subscription-account__detail"
      aria-live="polite"
    >
      <div v-if="!detailLoaded && !detailError" class="subscription-account__skeleton">
        <div class="subscription-account__skeleton-section">
          <span class="subscription-account__skeleton-title">
            <SkeletonBlock width="88px" height="11px" />
          </span>
          <SkeletonBlock height="var(--subscription-detail-activity-height)" />
        </div>
        <div v-if="supportsQuotaObservation" class="subscription-account__skeleton-section">
          <span class="subscription-account__skeleton-title">
            <SkeletonBlock width="140px" height="11px" />
          </span>
          <SkeletonBlock :height="windowSkeletonHeight" />
        </div>
        <div class="subscription-account__skeleton-section">
          <span class="subscription-account__skeleton-title">
            <SkeletonBlock width="88px" height="11px" />
          </span>
          <SkeletonBlock height="var(--subscription-detail-diagnostics-height)" />
        </div>
      </div>

      <div v-else-if="detailError" class="subscription-account__detail-error" role="alert">
        <span>{{ detailError }}</span>
        <AppButton variant="secondary" size="compact" :disabled="busy" @click="retryDetails">
          {{ t('common.retry') }}
        </AppButton>
      </div>

      <div v-else class="subscription-account__detail-content">
        <section class="subscription-account__detail-section">
          <h3>{{ t('group.credentials.subscription.activity') }}</h3>
          <div class="subscription-account__activity">
            <span class="subscription-account__activity-last">
              {{ t('group.credentials.subscription.lastUsed') }}
              <AppRelativeTime
                :instant="item.last_used_at_ms ?? null"
                :locale="locale"
                :empty-label="t('group.credentials.subscription.unknown')"
                hint
              />
            </span>
            <span v-if="dailyUsage" class="subscription-account__activity-daily">
              <span class="subscription-account__activity-window">
                {{ t('group.credentials.subscription.dailyWindow') }}
              </span>
              <span :title="dailyIncompleteHint">
                {{ t('group.credentials.subscription.dailySuccess') }}
                <strong class="subscription-account__daily-success">{{
                  n(dailyUsage.success_count)
                }}</strong>
              </span>
              <span :title="dailyIncompleteHint">
                {{ t('group.credentials.subscription.dailyFailure') }}
                <strong class="subscription-account__daily-failure">{{
                  n(dailyUsage.failure_count)
                }}</strong>
              </span>
            </span>
            <span v-else class="subscription-account__activity-daily">
              {{ t('group.credentials.subscription.estimate.unavailable') }}
            </span>
          </div>
        </section>

        <section v-if="supportsQuotaObservation" class="subscription-account__detail-section">
          <h3>{{ t('group.credentials.subscription.estimate.title') }}</h3>
          <div class="subscription-account__window-table" role="table">
            <div
              class="subscription-account__window-row subscription-account__window-head"
              role="row"
            >
              <span role="columnheader">{{
                t('group.credentials.subscription.estimate.window')
              }}</span>
              <span role="columnheader">{{
                t('group.credentials.subscription.estimate.used')
              }}</span>
              <span role="columnheader">{{
                t('group.credentials.subscription.estimate.requests')
              }}</span>
              <span role="columnheader">{{
                t('group.credentials.subscription.estimate.tokens')
              }}</span>
              <span role="columnheader">{{
                t('group.credentials.subscription.estimate.referenceCost')
              }}</span>
            </div>
            <div
              v-for="window in accountQuotaWindows"
              :key="window.id"
              class="subscription-account__window-row"
              role="row"
            >
              <OverflowTooltip
                class="subscription-account__window-name"
                :content="window.label"
                role="cell"
              >
                {{ window.label }}
              </OverflowTooltip>
              <span
                class="subscription-account__window-value subscription-account__window-used"
                role="cell"
              >
                {{ usedPercentValue(window) }}
              </span>
              <span
                class="subscription-account__window-value"
                :title="requestCountTitle(window)"
                role="cell"
              >
                {{ window.observed_usage ? n(window.observed_usage.request_count) : '—' }}
              </span>
              <span
                class="subscription-account__window-value"
                :title="tokenCountTitle(window)"
                role="cell"
              >
                {{
                  window.observed_usage
                    ? formatTokens(window.observed_usage.total_tokens, locale)
                    : '—'
                }}
              </span>
              <span
                class="subscription-account__window-value"
                :title="referenceCostTitle(window)"
                role="cell"
              >
                {{
                  window.observed_usage
                    ? formatEstimatedCost(
                        window.observed_usage.estimated_reference_cost_nano_usd,
                        locale,
                      )
                    : '—'
                }}
              </span>
            </div>
            <p v-if="accountQuotaWindows.length === 0" class="subscription-account__window-empty">
              {{ t('group.credentials.subscription.estimate.unavailable') }}
            </p>
          </div>
        </section>

        <section class="subscription-account__detail-section">
          <h3>{{ t('group.credentials.subscription.diagnostics') }}</h3>
          <div class="subscription-account__diagnostics">
            <dl>
              <dt>{{ t('group.credentials.detailsFailure') }}</dt>
              <dd>{{ failureLabel }}</dd>
            </dl>
            <dl v-if="supportsQuotaObservation">
              <dt>{{ t('group.credentials.subscription.freshUntil') }}</dt>
              <dd>
                <AppRelativeTime
                  :instant="observation?.fresh_until_ms ?? null"
                  :locale="locale"
                  :empty-label="t('group.credentials.subscription.unknown')"
                  hint
                />
              </dd>
            </dl>
            <dl>
              <dt>{{ t('group.credentials.subscription.lastTokenRefresh') }}</dt>
              <dd>
                <AppRelativeTime
                  :instant="item.account.last_refresh_at_ms ?? null"
                  :locale="locale"
                  :empty-label="t('group.credentials.subscription.unknown')"
                  hint
                />
              </dd>
            </dl>
            <dl>
              <dt>{{ t('group.credentials.detailsConsecutive') }}</dt>
              <dd>{{ n(item.consecutive_failure_count) }}</dd>
            </dl>
            <dl v-if="supportsQuotaObservation">
              <dt>{{ t('group.credentials.subscription.lastError') }}</dt>
              <dd>{{ observationErrorLabel(observation?.last_error_code) }}</dd>
            </dl>
            <dl>
              <dt>{{ t('group.credentials.subscription.tokenExpiresAt') }}</dt>
              <dd>
                <AppRelativeTime
                  :instant="item.account.expires_at_ms ?? null"
                  :locale="locale"
                  :empty-label="t('group.credentials.subscription.unknown')"
                  :tooltip-content="credentialExpiryTooltip"
                  hint
                />
              </dd>
            </dl>
          </div>
          <div
            v-if="supportsQuotaObservation && constrainedModels.length"
            class="subscription-account__models"
          >
            <span>{{ t('group.credentials.subscription.modelConstraints') }}</span>
            <code v-for="model in constrainedModels" :key="model">{{ model }}</code>
          </div>
          <div
            v-if="supportsResetCredit && resetCredits.length"
            class="subscription-account__reset-credit-list"
          >
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
        </section>
      </div>
    </section>
  </article>
</template>

<style scoped>
.subscription-account {
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--color-action) 24%, var(--color-border-subtle));
  border-left: 3px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: color-mix(in srgb, var(--color-action-soft) 46%, var(--color-surface));
  box-shadow:
    0 1px 2px color-mix(in srgb, var(--color-action) 14%, transparent),
    0 8px 24px color-mix(in srgb, var(--color-action) 10%, transparent);
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
.subscription-account__main {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4) 0;
}
.subscription-account__top {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}
.subscription-account__mail {
  min-width: 0;
  overflow: hidden;
  margin-right: 2px;
  font-family: var(--font-mono);
  font-size: var(--text-body);
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-account__plan {
  display: inline-flex;
  min-height: 24px;
  flex: none;
  align-items: center;
  border-radius: var(--radius-tag);
  background: var(--color-action-soft);
  color: var(--color-action);
  padding: 3px 8px;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: 600;
  line-height: 1;
  white-space: nowrap;
}
.subscription-account__actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--space-2);
  margin-left: auto;
}
.subscription-account__sync-age {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}
.subscription-account__sync-button :deep(.app-button),
.subscription-account__sync-button.app-button {
  min-height: 30px;
  padding-inline: 10px;
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
  grid-template-columns: minmax(72px, 98px) minmax(0, 1fr) max-content 64px;
  align-items: center;
  gap: var(--space-3);
}
.subscription-account__quota-name {
  overflow: hidden;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-account__quota-track {
  position: relative;
  height: 8px;
  overflow: hidden;
  border-radius: 999px;
  background: light-dark(#e9edf1, #29313a);
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
  font-weight: 650;
  font-variant-numeric: tabular-nums;
  text-align: right;
  white-space: nowrap;
}
.subscription-account__quota-reset {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
  text-align: right;
  white-space: nowrap;
}
.subscription-account__faint,
.subscription-account__hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
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
.subscription-account__credits > span:first-child,
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
  padding: 8px 10px;
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
.subscription-account__detail-control {
  display: grid;
  grid-template-columns: 1fr 44px 1fr;
  align-items: center;
  margin-top: 2px;
}
.subscription-account__detail-control::before,
.subscription-account__detail-control::after {
  height: 1px;
  background: var(--color-border-subtle);
  content: '';
}
.subscription-account__detail-toggle {
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border: 0;
  appearance: none;
  background: transparent;
  color: var(--color-text-faint);
  cursor: pointer;
}
.subscription-account__detail-disc {
  display: grid;
  width: 30px;
  height: 30px;
  place-items: center;
  border: 1px solid var(--color-border-control);
  border-radius: 50%;
  background: var(--color-surface);
  color: var(--color-text-muted);
  box-shadow: 0 1px 2px rgb(24 29 33 / 5%);
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    color var(--duration-fast) var(--easing-standard);
}
.subscription-account__detail-toggle:hover:not(:disabled) .subscription-account__detail-disc {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
}
.subscription-account__detail-toggle:disabled {
  cursor: wait;
}
.subscription-account__detail-toggle:disabled .subscription-account__detail-disc {
  opacity: 0.66;
}
.subscription-account__detail-toggle svg {
  width: 15px;
  height: 15px;
}
.subscription-account__detail-spinner {
  animation: subscription-account-spin 800ms linear infinite;
}
.subscription-account__detail {
  border-top: 1px solid color-mix(in srgb, var(--color-action) 18%, var(--color-border-subtle));
  background: color-mix(in srgb, var(--color-action-soft) 72%, var(--color-surface));
  padding: 14px 18px 16px;
}
.subscription-account__skeleton {
  --subscription-detail-activity-height: 34px;
  --subscription-detail-diagnostics-height: 61px;
}
.subscription-account__skeleton,
.subscription-account__detail-content {
  display: grid;
  gap: 13px;
}
.subscription-account__skeleton-section {
  display: grid;
  gap: 6px;
}
.subscription-account__skeleton-title {
  display: flex;
  height: 15px;
  align-items: center;
}
.subscription-account__detail-section {
  min-width: 0;
}
.subscription-account__detail-section h3 {
  margin: 0 0 6px;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 680;
  letter-spacing: 0.06em;
}
.subscription-account__activity {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: baseline;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 7px 10px;
}
.subscription-account__activity-last {
  color: var(--color-text-muted);
}
.subscription-account__activity-last :deep(.app-relative-time) {
  margin-left: var(--space-1);
  color: var(--color-text);
  font-family: var(--font-mono);
}
.subscription-account__activity-daily {
  display: flex;
  align-items: baseline;
  gap: 9px;
  color: var(--color-text-muted);
  white-space: nowrap;
}
.subscription-account__activity-window {
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  padding: 1px 6px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-account__activity-daily strong {
  margin-left: 3px;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-variant-numeric: tabular-nums;
}
.subscription-account__daily-success {
  color: var(--color-success);
}
.subscription-account__daily-failure {
  color: var(--color-danger);
}
.subscription-account__window-table {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.subscription-account__window-row {
  display: grid;
  grid-template-columns: minmax(82px, 1.35fr) repeat(4, minmax(62px, 1fr));
  min-height: 34px;
  align-items: center;
  column-gap: 10px;
  padding: 0 10px;
}
.subscription-account__window-row + .subscription-account__window-row {
  border-top: 1px solid var(--color-border-subtle);
}
.subscription-account__window-head {
  min-height: 26px;
  background: var(--color-surface-sunken);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 620;
}
.subscription-account__window-row > :not(:first-child) {
  text-align: right;
}
.subscription-account__window-name {
  min-width: 0;
  overflow: hidden;
  font-weight: 620;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-account__window-value {
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.subscription-account__window-used {
  color: var(--color-warning);
  font-weight: 650;
}
.subscription-account__window-empty {
  margin: 0;
  padding: 9px 10px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-account__diagnostics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1px;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-border-subtle);
}
.subscription-account__diagnostics dl {
  display: flex;
  min-width: 0;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
  margin: 0;
  background: var(--color-surface);
  padding: 7px 9px;
}
.subscription-account__diagnostics dt {
  flex: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-account__diagnostics dd {
  min-width: 0;
  overflow: hidden;
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-account__models,
.subscription-account__reset-credit-list {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  margin-top: var(--space-2);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.subscription-account__models code {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface);
  padding: 2px 6px;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: inherit;
}
.subscription-account__detail-error {
  display: flex;
  min-height: 56px;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  color: var(--color-danger);
  font-size: var(--text-sm);
}
@keyframes subscription-account-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (max-width: 680px) {
  .subscription-account__main,
  .subscription-account__detail {
    padding-right: 14px;
    padding-left: 14px;
  }
  .subscription-account__top {
    flex-wrap: wrap;
  }
  .subscription-account__mail {
    width: 100%;
  }
  .subscription-account__actions {
    margin-left: auto;
  }
  .subscription-account__quota {
    grid-template-columns: 68px minmax(0, 1fr) max-content;
    gap: var(--space-2);
  }
  .subscription-account__quota-reset {
    grid-column: 2 / -1;
  }
  .subscription-account__activity {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-2);
  }
  .subscription-account__skeleton {
    --subscription-detail-activity-height: 60px;
    --subscription-detail-diagnostics-height: 92px;
  }
  .subscription-account__activity-daily {
    justify-content: space-between;
    gap: var(--space-1);
  }
  .subscription-account__window-row {
    grid-template-columns: 42px repeat(4, minmax(48px, 1fr));
    column-gap: 4px;
    padding: 0 7px;
  }
  .subscription-account__window-head,
  .subscription-account__window-value {
    font-size: 0.6875rem;
  }
  .subscription-account__diagnostics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (prefers-reduced-motion: reduce) {
  .subscription-account__detail-spinner {
    animation: none;
  }
}
</style>

<style>
.app-popover__content.app-popover__content--account-menu {
  width: auto;
  min-width: 180px;
  border-color: var(--color-border-control);
  border-radius: 10px;
  padding: 8px;
}
</style>
