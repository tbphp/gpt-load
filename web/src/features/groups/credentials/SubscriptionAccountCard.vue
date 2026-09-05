<script setup lang="ts">
import {
  Check,
  CircleCheck,
  CircleOff,
  Download,
  Ellipsis,
  Gauge,
  KeyRound,
  LoaderCircle,
  PencilLine,
  RefreshCw,
  RotateCcw,
  Trash2,
} from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  CredentialItemDto,
  ProxyMutation,
  CredentialQuotaLabelKey,
  CredentialQuotaWindowDto,
} from '@/api/control/types'
import type { ChannelCapabilitiesDto } from '@/app/resources/channels'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import ProxyConfigEditor from '@/components/config/ProxyConfigEditor.vue'
import ProxyScopeIndicator from '@/components/config/ProxyScopeIndicator.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatEstimatedCost, formatLocalInstant, formatTokens } from '@/lib/format'
import { quotaProgressTone } from '@/lib/quota-progress'

import { presentCredentialFailureCategory } from './credential-failure-presenter'

const props = withDefaults(
  defineProps<{
    item: CredentialItemDto
    selected: boolean
    busy: boolean
    refreshingObservation: boolean
    observationError: string
    detailBusy: boolean
    detailLoaded: boolean
    detailError: string
    channelIcon?: string
    channelMark?: string
    capabilities: ChannelCapabilitiesDto
    saveProxy: (value: ProxyMutation) => Promise<void>
  }>(),
  {
    channelIcon: undefined,
    channelMark: undefined,
  },
)
const emit = defineEmits<{
  'update:selected': [selected: boolean]
  toggle: [item: CredentialItemDto]
  restore: [item: CredentialItemDto]
  refresh: [item: CredentialItemDto]
  'load-details': [item: CredentialItemDto]
  reset: [item: CredentialItemDto]
  download: [item: CredentialItemDto]
  'refresh-credential': [item: CredentialItemDto]
  remove: [item: CredentialItemDto]
  weight: [payload: { item: CredentialItemDto; value: string }]
}>()
const { locale, n, t, te } = useI18n()
const menuOpen = ref(false)
const detailsExpanded = ref(false)
const proxyEditor = ref<{ beginEdit: () => void } | null>(null)
const weightEditing = ref(false)
const draftWeightMode = ref<'auto' | 'manual'>('auto')
const draftWeight = ref('50')
const weightInputId = computed(() => `subscription-account-weight-${props.item.credential_id}`)

// 自动权重等同代理的“继承”态，折叠时不加视觉噪音。
const showWeightChip = computed(
  () => props.item.weight_mode === 'manual' && props.item.weight !== null,
)
const weightModeOptions = computed(() => [
  { value: 'auto', label: t('group.credentials.weightEditor.auto'), disabled: props.busy },
  { value: 'manual', label: t('group.credentials.weightEditor.manual'), disabled: props.busy },
])
const manualWeightValid = computed(() => {
  if (draftWeightMode.value === 'auto') return true
  const value = Number(draftWeight.value)
  return Number.isInteger(value) && value >= 1 && value <= 100
})

function resetWeightDraft(): void {
  draftWeightMode.value = props.item.weight_mode
  draftWeight.value = String(props.item.weight ?? 50)
}

// 一次完成“展开 + 进入编辑”，与密钥列表点权重值一致。
function editWeight(): void {
  if (props.busy) return
  resetWeightDraft()
  detailsExpanded.value = true
  weightEditing.value = true
}

function editProxy(): void {
  if (props.busy) return
  detailsExpanded.value = true
  void nextTick(() => proxyEditor.value?.beginEdit())
}

function saveWeight(): void {
  if (props.busy || !manualWeightValid.value) return
  emit('weight', {
    item: props.item,
    value: draftWeightMode.value === 'auto' ? 'auto' : String(Number(draftWeight.value)),
  })
  weightEditing.value = false
}

// 收起卡片时退出编辑，避免下次展开停在旧草稿。
watch(
  () => [props.item.weight_mode, props.item.weight] as const,
  () => {
    if (!weightEditing.value) resetWeightDraft()
  },
  { immediate: true },
)
watch(detailsExpanded, (expanded) => {
  if (!expanded) weightEditing.value = false
})
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
const needsInitialQuotaSync = computed(
  () =>
    supportsQuotaObservation.value &&
    (observation.value === undefined ||
      (observation.value.state === 'unavailable' &&
        observation.value.observed_at_ms === null &&
        observation.value.last_attempt_at_ms === null)),
)
const supportsResetCredit = computed(() =>
  props.capabilities.credential_actions.includes('reset_credit'),
)
const snapshot = computed(() => observation.value?.snapshot)
function isAccountWideQuotaWindow(window: CredentialQuotaWindowDto): boolean {
  return window.scope === 'account'
}

function quotaWindowDuration(window: CredentialQuotaWindowDto): number {
  const seconds = window.window_seconds
  return seconds !== undefined && Number.isFinite(seconds) && seconds > 0
    ? seconds
    : Number.MAX_SAFE_INTEGER
}

function quotaWindowGroupKey(window: CredentialQuotaWindowDto): string {
  const modelIDs = (window.model_ids ?? [])
    .map((model) => model.trim())
    .filter((model) => model !== '')
    .sort()
  if (modelIDs.length > 0) return `models:${modelIDs.join('\u0000')}`

  const period = quotaWindowPeriodLabel(window.window_seconds)
  const labelParts = window.label
    .split('·')
    .map((part) => part.trim())
    .filter((part) => part !== '')
  const finalPart = labelParts.at(-1)
  if (
    period &&
    labelParts.length > 1 &&
    finalPart !== undefined &&
    normalizedQuotaLabelPart(finalPart) === normalizedQuotaLabelPart(period)
  ) {
    const subject = normalizedQuotaLabelPart(labelParts.slice(0, -1).join(' '))
    if (subject) return `subject:${subject}`
  }
  return `scope:${normalizedQuotaLabelPart(window.scope) || window.id}`
}

// 呈现层统一排序：账号全局窗口优先；其余按同一模型/专属窗口成组，组内按时长升序。
const quotaWindows = computed(() => {
  const windows = snapshot.value?.quota_windows ?? []
  const groupOrder = new Map<string, number>()
  for (const [index, window] of windows.entries()) {
    const key = quotaWindowGroupKey(window)
    if (!groupOrder.has(key)) groupOrder.set(key, index)
  }
  return [...windows].sort((left, right) => {
    const scopeDifference =
      Number(!isAccountWideQuotaWindow(left)) - Number(!isAccountWideQuotaWindow(right))
    if (scopeDifference !== 0) return scopeDifference
    const groupDifference =
      (groupOrder.get(quotaWindowGroupKey(left)) ?? 0) -
      (groupOrder.get(quotaWindowGroupKey(right)) ?? 0)
    if (groupDifference !== 0) return groupDifference
    return quotaWindowDuration(left) - quotaWindowDuration(right)
  })
})
const accountQuotaWindows = computed(() =>
  quotaWindows.value.filter((window) => window.scope === 'account'),
)
const usageQuotaWindows = computed(() =>
  accountQuotaWindows.value.filter((window) => quotaWindowPeriod(window) !== undefined),
)
const hasUsageQuotaWindows = computed(() => usageQuotaWindows.value.length > 0)
const windowSkeletonHeight = computed(() => `${24 + usageQuotaWindows.value.length * 32}px`)
const refreshSkeletonRows = computed(() => Math.min(4, quotaWindows.value.length))
const constrainedModels = computed(() =>
  Array.from(new Set(quotaWindows.value.flatMap((window) => window.model_ids ?? []))),
)

const quotaSubjectKeys: Readonly<Record<string, CredentialQuotaLabelKey>> = {
  session: 'session',
  weekly: 'weekly',
  'extra usage': 'extra_usage',
  'included usage': 'included_usage',
  'pay as you go': 'pay_as_you_go',
  'oauth apps': 'oauth_apps',
}
const accountName = computed(() => props.item.account.email ?? props.item.mask)
const planLabel = computed(() => {
  const plan = snapshot.value?.plan_summary.name?.trim()
  return plan ?? ''
})
const planLevel = computed(() => snapshot.value?.plan_summary.level ?? 'unknown')
const credentialExpiryTooltip = computed(() => {
  const expiresAtMS = props.item.account.expires_at_ms
  if (expiresAtMS === undefined) return undefined
  const exact = formatLocalInstant(expiresAtMS, locale.value)
  return props.item.auth_state === 'ready'
    ? `${exact}\n${t('group.credentials.subscription.autoRenews')}`
    : exact
})
const syncTimeTooltip = computed(() => {
  const observedAtMS = observation.value?.observed_at_ms
  if (observedAtMS === undefined || observedAtMS === null) return undefined
  return t('group.credentials.subscription.syncTime', {
    time: formatLocalInstant(observedAtMS, locale.value),
  })
})
const syncExactTimeTooltip = computed(() => {
  const observedAtMS = observation.value?.observed_at_ms
  if (observedAtMS === undefined || observedAtMS === null) return undefined
  return formatLocalInstant(observedAtMS, locale.value)
})
const quotaResetPrefix = computed(() => t('group.credentials.subscription.quotaResetPrefix'))
const quotaResetSuffix = computed(() => t('group.credentials.subscription.quotaResetSuffix'))
const resetCreditsAvailable = computed(() => snapshot.value?.reset_credits_available ?? 0)
const resetCredits = computed(() => snapshot.value?.reset_credits ?? [])
const availableResetCreditDetails = computed(() =>
  resetCredits.value.slice(0, resetCreditsAvailable.value),
)
const hasResetCredits = computed(() => supportsResetCredit.value && resetCreditsAvailable.value > 0)
type ResetCreditDotTone = 'default' | 'warning' | 'danger'
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
const nearestResetCredit = computed(() =>
  availableResetCreditDetails.value.reduce<(typeof resetCredits.value)[number] | undefined>(
    (nearest, credit) => {
      if (credit.expires_at_ms === undefined || credit.expires_at_ms <= nowMs.value) return nearest
      return nearest === undefined ||
        nearest.expires_at_ms === undefined ||
        credit.expires_at_ms < nearest.expires_at_ms
        ? credit
        : nearest
    },
    undefined,
  ),
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
const isProblem = computed(
  () => props.item.effective_status === 'cooldown' || props.item.effective_status === 'blacklisted',
)

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

function normalizedQuotaLabelPart(value: string): string {
  return value.trim().toLowerCase().replaceAll('_', ' ').replaceAll('-', ' ')
}

function translatedQuotaLabel(labelKey: CredentialQuotaLabelKey, fallback: string): string {
  const key = `group.credentials.subscription.quotaLabels.${labelKey}`
  return te(key) ? t(key) : fallback
}

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

type UnifiedStatus =
  | 'available'
  | 'quota_exhausted'
  | 'cooldown'
  | 'blacklisted'
  | 'refreshing'
  | 'needs_reauth'
  | 'outcome_unknown'
  | 'disabled'

const unifiedStatus = computed<UnifiedStatus>(() => {
  if (props.item.configured_status === 'disabled') return 'disabled'
  if (props.item.auth_state === 'refreshing') return 'refreshing'
  if (props.item.auth_state === 'reauthorization_required') return 'needs_reauth'
  if (props.item.auth_state === 'outcome_unknown') return 'outcome_unknown'
  if (props.item.effective_status === 'blacklisted') return 'blacklisted'
  if (props.item.effective_status === 'cooldown') return 'cooldown'
  if (
    supportsQuotaObservation.value &&
    quotaWindows.value.some((window) => window.scope === 'account' && window.state === 'exhausted')
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
    outcome_unknown: 'danger',
    disabled: 'neutral',
  }
  return tones[unifiedStatus.value]
})
const statusLabel = computed(() =>
  t(`group.credentials.subscription.status.${unifiedStatus.value}`),
)
const displayDisabled = computed(() => unifiedStatus.value === 'disabled')
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
const credentialRefreshBlocked = computed(() => props.item.auth_state !== 'ready')
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
  observation_access_denied: 'group.credentials.subscription.observationError.accessDenied',
  observation_authorization_failed:
    'group.credentials.subscription.observationError.authorizationFailed',
  observation_partial: 'group.credentials.subscription.observationError.partial',
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
  if (window.utilization !== undefined) return Math.round((1 - window.utilization) * 100)
  if (window.remaining !== undefined && window.limit && window.limit > 0) {
    return Math.max(0, Math.min(100, Math.round((window.remaining / window.limit) * 100)))
  }
  return undefined
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
    return t('group.credentials.subscription.remainingAmount', {
      remaining: n(window.remaining),
    })
  }
  return t('group.credentials.subscription.unknown')
}

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

function usedPercentValue(window: CredentialQuotaWindowDto): string {
  const remaining = remainingPercent(window)
  return remaining === undefined ? '—' : `${n(100 - remaining)}%`
}

function quotaTone(window: CredentialQuotaWindowDto): 'success' | 'warning' | 'danger' | undefined {
  const value = remainingPercent(window)
  if (value === undefined) return undefined
  return quotaProgressTone(value, window.state === 'exhausted')
}

function quotaFillStyle(window: CredentialQuotaWindowDto): Record<string, string> {
  const value = remainingPercent(window)
  return value === undefined ? {} : { width: `${value}%` }
}

function toggleDetails(): void {
  if (props.detailBusy) return
  detailsExpanded.value = !detailsExpanded.value
}

function retryDetails(): void {
  emit('load-details', props.item)
}

function runMenuAction(
  action: 'download' | 'refresh-credential' | 'toggle' | 'restore' | 'remove',
): void {
  menuOpen.value = false
  switch (action) {
    case 'download':
      emit('download', props.item)
      return
    case 'refresh-credential':
      emit('refresh-credential', props.item)
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
  <article
    class="subscription-account"
    :class="[
      `subscription-account--${statusTone}`,
      {
        'subscription-account--disabled': displayDisabled,
        'subscription-account--refreshing': refreshingObservation,
      },
    ]"
    :aria-busy="refreshingObservation ? 'true' : undefined"
  >
    <div
      v-if="refreshingObservation"
      class="subscription-account__refresh-skeleton"
      role="status"
      aria-live="polite"
    >
      <span class="sr-only">{{ t('group.credentials.subscription.syncingQuota') }}</span>
      <div class="subscription-account__refresh-skeleton-header">
        <div class="subscription-account__refresh-skeleton-top" aria-hidden="true">
          <div>
            <SkeletonBlock width="20px" height="20px" />
            <SkeletonBlock width="92px" height="24px" />
            <SkeletonBlock width="64px" height="24px" />
          </div>
          <div>
            <SkeletonBlock width="58px" height="12px" />
            <SkeletonBlock width="32px" height="32px" />
            <SkeletonBlock width="32px" height="32px" />
          </div>
        </div>
        <SkeletonBlock width="62%" height="20px" aria-hidden="true" />
      </div>
      <div
        v-if="supportsQuotaObservation && quotaWindows.length"
        class="subscription-account__refresh-skeleton-quotas"
        aria-hidden="true"
      >
        <div
          v-for="index in refreshSkeletonRows"
          :key="index"
          class="subscription-account__refresh-skeleton-quota"
        >
          <SkeletonBlock width="44%" height="13px" />
          <span>
            <SkeletonBlock width="64px" height="12px" />
            <SkeletonBlock width="82px" height="12px" />
          </span>
        </div>
      </div>
      <SkeletonBlock
        v-else-if="supportsQuotaObservation"
        class="subscription-account__refresh-skeleton-empty-quota"
        width="48%"
        height="16px"
        aria-hidden="true"
      />
      <SkeletonBlock
        v-if="unifiedStatus === 'quota_exhausted'"
        class="subscription-account__refresh-skeleton-hint"
        width="82%"
        height="16px"
        aria-hidden="true"
      />
      <div
        v-if="hasResetCredits"
        class="subscription-account__refresh-skeleton-credits"
        aria-hidden="true"
      >
        <SkeletonBlock width="52px" height="14px" />
        <SkeletonBlock width="72px" height="14px" />
        <SkeletonBlock
          v-if="nearestResetCredit && nearestResetCredit.expires_at_ms !== undefined"
          class="subscription-account__refresh-skeleton-credits-expiry"
          width="68px"
          height="14px"
        />
        <SkeletonBlock width="26px" height="26px" />
      </div>
      <div class="subscription-account__refresh-skeleton-detail-control" aria-hidden="true">
        <span></span>
        <SkeletonBlock width="30px" height="30px" />
        <span></span>
      </div>
      <div
        v-if="detailsExpanded"
        class="subscription-account__refresh-skeleton-detail"
        aria-hidden="true"
      >
        <div
          v-if="supportsQuotaObservation && hasUsageQuotaWindows"
          class="subscription-account__skeleton-section"
        >
          <span class="subscription-account__skeleton-title">
            <SkeletonBlock width="140px" height="11px" />
          </span>
          <SkeletonBlock :height="windowSkeletonHeight" />
        </div>
        <div class="subscription-account__skeleton-section">
          <span class="subscription-account__skeleton-title">
            <SkeletonBlock width="88px" height="11px" />
          </span>
          <SkeletonBlock height="var(--subscription-detail-overview-height)" />
        </div>
      </div>
    </div>
    <div class="subscription-account__main">
      <header class="subscription-account__top">
        <div class="subscription-account__top-row">
          <label class="subscription-account__select">
            <span class="sr-only">{{
              t('group.credentials.subscription.selectAccount', { account: accountName })
            }}</span>
            <input
              type="checkbox"
              :checked="selected"
              :disabled="busy"
              @change="emit('update:selected', ($event.target as HTMLInputElement).checked)"
            />
            <span class="subscription-account__select-box" aria-hidden="true">
              <Check v-if="selected" :size="16" stroke-width="2.5" />
            </span>
          </label>
          <div class="subscription-account__badges">
            <span
              v-if="planLabel"
              class="subscription-account__plan"
              :class="`subscription-account__plan--${planLevel}`"
            >
              <ChannelIcon
                v-if="channelIcon && channelMark"
                class="subscription-account__plan-icon"
                :icon="channelIcon"
                :mark="channelMark"
              />
              <span>{{ planLabel }}</span>
            </span>
            <StatusBadge
              class="subscription-account__status"
              :tone="statusTone"
              :icon="displayDisabled ? 'off' : undefined"
              size="compact"
            >
              {{ statusLabel }}
            </StatusBadge>
            <button
              v-if="showWeightChip"
              class="subscription-account__weight-chip"
              type="button"
              :disabled="busy"
              :title="t('group.credentials.editWeightHint')"
              :aria-label="t('group.credentials.editWeight')"
              @click="editWeight"
            >
              <Gauge :size="12" aria-hidden="true" />
              <b>{{ n(item.weight as number) }}</b>
            </button>
            <ProxyScopeIndicator
              v-if="capabilities.outbound_proxy"
              :view="item.proxy"
              clickable
              @activate="editProxy"
            />
          </div>
          <div class="subscription-account__actions">
            <span
              v-if="supportsQuotaObservation && observation?.observed_at_ms != null"
              class="subscription-account__sync-age"
            >
              <AppRelativeTime
                :instant="observation.observed_at_ms"
                :locale="locale"
                :empty-label="t('group.credentials.subscription.unknown')"
                :tooltip-content="syncTimeTooltip"
                hint
              />
            </span>
            <AppTooltip
              v-if="supportsQuotaObservation"
              :content="t('group.credentials.subscription.sync')"
            >
              <IconButton
                class="subscription-account__sync-button"
                size="compact"
                variant="ghost"
                :label="t('group.credentials.subscription.sync')"
                :busy="refreshingObservation"
                :disabled="busy || credentialRefreshBlocked"
                @click="emit('refresh', item)"
              >
                <RefreshCw
                  :class="{ 'subscription-account__sync-icon--spinning': refreshingObservation }"
                  :size="15"
                  aria-hidden="true"
                />
              </IconButton>
            </AppTooltip>
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
                <button type="button" :disabled="busy" @click="runMenuAction('download')">
                  <Download :size="15" aria-hidden="true" />{{
                    t('group.credentials.subscription.download')
                  }}
                </button>
                <button
                  type="button"
                  :disabled="
                    busy || item.configured_status === 'disabled' || credentialRefreshBlocked
                  "
                  @click="runMenuAction('refresh-credential')"
                >
                  <KeyRound :size="15" aria-hidden="true" />{{
                    t('group.credentials.subscription.refreshCredential')
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
        </div>
        <div class="subscription-account__top-row">
          <OverflowTooltip class="subscription-account__mail" :content="accountName">
            {{ accountName }}
          </OverflowTooltip>
        </div>
      </header>

      <div v-if="authIssue" class="subscription-account__alert">
        <span>{{ authIssue }}</span>
      </div>
      <div v-if="observationError" class="subscription-account__alert" role="alert">
        <span>{{ observationError }}</span>
      </div>

      <div
        v-if="supportsQuotaObservation && quotaWindows.length"
        class="subscription-account__quotas"
      >
        <div
          v-for="window in quotaWindows"
          :key="window.id"
          class="subscription-account__quota"
          :class="`subscription-account__quota--${quotaTone(window) ?? 'unknown'}`"
        >
          <span
            class="subscription-account__quota-meter"
            :role="remainingPercent(window) === undefined ? 'img' : 'progressbar'"
            :aria-label="
              remainingPercent(window) === undefined
                ? `${quotaWindowLabel(window)}: ${quotaValueLabel(window)}`
                : quotaWindowLabel(window)
            "
            :aria-valuemin="remainingPercent(window) === undefined ? undefined : 0"
            :aria-valuemax="remainingPercent(window) === undefined ? undefined : 100"
            :aria-valuenow="remainingPercent(window)"
            :aria-valuetext="
              remainingPercent(window) === undefined ? undefined : quotaValueLabel(window)
            "
          >
            <span
              v-if="remainingPercent(window) !== undefined"
              class="subscription-account__quota-fill"
              :style="quotaFillStyle(window)"
              aria-hidden="true"
            ></span>
          </span>
          <OverflowTooltip
            class="subscription-account__quota-name"
            :content="quotaWindowLabel(window)"
          >
            {{ quotaWindowLabel(window) }}
          </OverflowTooltip>
          <span class="subscription-account__quota-meta">
            <strong>{{ quotaValueLabel(window) }}</strong>
            <span aria-hidden="true">·</span>
            <span class="subscription-account__quota-reset">
              <AppTooltip
                v-if="window.reset_at_ms && quotaWindowNeedsRefresh(window)"
                :content="quotaPeriodTooltip(window) ?? ''"
              >
                <span class="subscription-account__quota-reset-pending" tabindex="0">
                  {{ t('group.credentials.subscription.quotaPendingRefresh') }}
                </span>
              </AppTooltip>
              <span v-else-if="window.reset_at_ms" class="subscription-account__quota-reset-time">
                <span v-if="quotaResetPrefix" class="subscription-account__quota-reset-prefix">
                  {{ quotaResetPrefix }}
                </span>
                <AppRelativeTime
                  :instant="window.reset_at_ms"
                  :locale="locale"
                  :empty-label="t('group.credentials.subscription.unknown')"
                  :tooltip-content="quotaPeriodTooltip(window)"
                  hint
                /><span v-if="quotaResetSuffix">{{ quotaResetSuffix }}</span>
              </span>
              <template v-else>—</template>
            </span>
          </span>
        </div>
      </div>
      <div v-else-if="needsInitialQuotaSync" class="subscription-account__initial-sync">
        <span class="subscription-account__initial-sync-icon" aria-hidden="true">
          <Gauge :size="17" />
        </span>
        <span class="subscription-account__initial-sync-copy">
          <strong>{{ t('group.credentials.subscription.initialSyncTitle') }}</strong>
          <span>{{ t('group.credentials.subscription.initialSyncDescription') }}</span>
        </span>
        <AppButton
          class="subscription-account__initial-sync-action"
          variant="secondary"
          tone="action"
          size="compact"
          :disabled="busy || credentialRefreshBlocked"
          @click="emit('refresh', item)"
        >
          <RefreshCw :size="14" aria-hidden="true" />
          {{ t('group.credentials.subscription.initialSyncAction') }}
        </AppButton>
      </div>
      <p v-else-if="supportsQuotaObservation" class="subscription-account__faint">
        {{ t('group.credentials.subscription.noQuota') }}
      </p>

      <p v-if="unifiedStatus === 'quota_exhausted'" class="subscription-account__hint">
        {{ t('group.credentials.subscription.quotaExhaustedHint') }}
      </p>

      <div v-if="hasResetCredits" class="subscription-account__credits">
        <span>{{ t('group.credentials.subscription.resetCredits') }}</span>
        <AppTooltip :content="resetCreditsTooltip">
          <span
            class="subscription-account__credits-summary"
            tabindex="0"
            :aria-label="resetCreditsTooltip"
          >
            <span class="subscription-account__credits-dots" aria-hidden="true">
              <i
                v-for="dot in resetCreditDots"
                :key="dot.index"
                :class="`subscription-account__credits-dot--${dot.tone}`"
              ></i>
            </span>
            <strong>{{
              t('group.credentials.subscription.resetCreditsCount', {
                count: n(resetCreditsAvailable),
              })
            }}</strong>
          </span>
        </AppTooltip>
        <span
          v-if="nearestResetCredit && nearestResetCredit.expires_at_ms !== undefined"
          class="subscription-account__credits-expiry"
        >
          {{ t('group.credentials.subscription.nearestResetCredit') }}
          <AppRelativeTime
            :instant="nearestResetCredit.expires_at_ms"
            :locale="locale"
            :empty-label="t('group.credentials.subscription.unknown')"
            hint
          />
        </span>
        <span class="subscription-account__spacer"></span>
        <AppTooltip :content="t('group.credentials.subscription.resetCreditsActionTooltip')">
          <AppButton
            class="subscription-account__credits-action"
            variant="ghost"
            size="compact"
            :disabled="busy"
            :aria-label="t('group.credentials.subscription.resetCreditsActionTooltip')"
            @click="emit('reset', item)"
          >
            <RotateCcw :size="15" aria-hidden="true" />
          </AppButton>
        </AppTooltip>
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
        <div
          v-if="supportsQuotaObservation && hasUsageQuotaWindows"
          class="subscription-account__skeleton-section"
        >
          <span class="subscription-account__skeleton-title">
            <SkeletonBlock width="140px" height="11px" />
          </span>
          <SkeletonBlock :height="windowSkeletonHeight" />
        </div>
        <div class="subscription-account__skeleton-section">
          <span class="subscription-account__skeleton-title">
            <SkeletonBlock width="88px" height="11px" />
          </span>
          <SkeletonBlock height="var(--subscription-detail-overview-height)" />
        </div>
      </div>

      <div v-else-if="detailError" class="subscription-account__detail-error" role="alert">
        <span>{{ detailError }}</span>
        <AppButton variant="secondary" size="compact" :disabled="busy" @click="retryDetails">
          {{ t('common.retry') }}
        </AppButton>
      </div>

      <div v-else class="subscription-account__detail-content">
        <section
          v-if="supportsQuotaObservation && hasUsageQuotaWindows"
          class="subscription-account__detail-section"
        >
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
              v-for="window in usageQuotaWindows"
              :key="window.id"
              class="subscription-account__window-row"
              role="row"
            >
              <OverflowTooltip
                class="subscription-account__window-name"
                :content="quotaWindowLabel(window)"
                role="cell"
              >
                {{ quotaWindowLabel(window) }}
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
          </div>
        </section>

        <section class="subscription-account__detail-section">
          <h3>{{ t('group.credentials.subscription.overview') }}</h3>
          <div class="subscription-account__diagnostics">
            <dl>
              <dt>{{ t('group.credentials.subscription.lastUsed') }}</dt>
              <dd>
                <AppRelativeTime
                  :instant="item.last_used_at_ms ?? null"
                  :locale="locale"
                  :empty-label="t('group.credentials.subscription.unknown')"
                  hint
                />
              </dd>
            </dl>
            <dl>
              <dt>{{ t('group.credentials.subscription.dailySuccessSummary') }}</dt>
              <dd
                :class="{ 'subscription-account__daily-success': dailyUsage }"
                :title="dailyIncompleteHint"
              >
                {{
                  dailyUsage
                    ? n(dailyUsage.success_count)
                    : t('group.credentials.subscription.estimate.unavailable')
                }}
              </dd>
            </dl>
            <dl>
              <dt>{{ t('group.credentials.subscription.dailyFailureSummary') }}</dt>
              <dd
                :class="{ 'subscription-account__daily-failure': dailyUsage }"
                :title="dailyIncompleteHint"
              >
                {{
                  dailyUsage
                    ? n(dailyUsage.failure_count)
                    : t('group.credentials.subscription.estimate.unavailable')
                }}
              </dd>
            </dl>
            <dl>
              <dt>{{ t('group.credentials.detailsFailure') }}</dt>
              <dd>{{ failureLabel }}</dd>
            </dl>
            <dl>
              <dt>{{ t('group.credentials.detailsConsecutive') }}</dt>
              <dd>{{ n(item.consecutive_failure_count) }}</dd>
            </dl>
            <dl>
              <dt>{{ t('group.credentials.subscription.lastError') }}</dt>
              <dd>{{ observationErrorLabel(observation?.last_error_code) }}</dd>
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
            <dl>
              <dt>{{ t('group.credentials.subscription.lastQuotaSync') }}</dt>
              <dd>
                <AppRelativeTime
                  :instant="observation?.observed_at_ms ?? null"
                  :locale="locale"
                  :empty-label="t('group.credentials.subscription.unknown')"
                  :tooltip-content="syncExactTimeTooltip"
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
        </section>
      </div>
      <div class="subscription-account__panels">
        <div class="setting-panel">
          <span class="setting-panel__title">{{ t('group.credentials.columns.weight') }}</span>
          <div class="setting-panel__body">
            <template v-if="!weightEditing">
              <span class="setting-panel__tag">
                {{ t(`group.credentials.weightEditor.${item.weight_mode}`) }}
              </span>
              <span class="setting-panel__value">
                {{ item.weight === null ? t('group.credentials.none') : n(item.weight) }}
              </span>
              <IconButton
                class="setting-panel__edit"
                variant="ghost"
                tone="action"
                size="xs"
                :label="t('group.credentials.editWeight')"
                :disabled="busy || displayDisabled"
                @click="editWeight"
              >
                <PencilLine :size="12" aria-hidden="true" />
              </IconButton>
            </template>
            <form v-else class="setting-panel__form" @submit.prevent="saveWeight">
              <SegmentedControl
                v-model="draftWeightMode"
                class="subscription-account__weight-mode"
                :label="t('group.credentials.weightEditor.mode')"
                :options="weightModeOptions"
                size="xs"
              />
              <label class="sr-only" :for="weightInputId">
                {{ t('group.credentials.weightEditor.value') }}
              </label>
              <input
                :id="weightInputId"
                v-model="draftWeight"
                class="subscription-account__weight-input"
                :class="{ 'is-concealed': draftWeightMode === 'auto' }"
                type="number"
                min="1"
                max="100"
                step="1"
                inputmode="numeric"
                :disabled="busy || draftWeightMode === 'auto'"
                :tabindex="draftWeightMode === 'auto' ? -1 : undefined"
                :aria-hidden="draftWeightMode === 'auto' ? 'true' : undefined"
                :aria-invalid="!manualWeightValid || undefined"
              />
              <div class="setting-panel__actions">
                <AppButton variant="ghost" size="compact" @click="weightEditing = false">
                  {{ t('group.credentials.weightEditor.cancel') }}
                </AppButton>
                <AppButton type="submit" size="compact" :disabled="busy || !manualWeightValid">
                  {{ t('group.credentials.weightEditor.save') }}
                </AppButton>
              </div>
              <p
                v-if="draftWeightMode === 'manual' && !manualWeightValid"
                class="setting-panel__error"
                role="alert"
              >
                {{ t('group.credentials.weightEditor.invalid') }}
              </p>
            </form>
          </div>
        </div>

        <ProxyConfigEditor
          ref="proxyEditor"
          :view="item.proxy"
          :save-proxy="saveProxy"
          :supported="capabilities.outbound_proxy"
          :disabled="busy"
        />
      </div>
    </section>
  </article>
</template>

<style scoped>
.subscription-account {
  container-type: inline-size;
  position: relative;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--color-action) 24%, var(--color-border-subtle));
  border-left: 3px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: color-mix(in srgb, var(--color-action-soft) 46%, var(--color-surface));
  box-shadow:
    0 1px 2px color-mix(in srgb, var(--color-action) 14%, transparent),
    0 8px 24px color-mix(in srgb, var(--color-action) 10%, transparent);
}
.subscription-account--refreshing > .subscription-account__main,
.subscription-account--refreshing > .subscription-account__detail {
  visibility: hidden;
}
.subscription-account__refresh-skeleton {
  --subscription-account-refresh-inline: var(--space-4);
  position: absolute;
  z-index: 2;
  inset: 0;
  display: grid;
  align-content: start;
  gap: var(--space-3);
  background: color-mix(in srgb, var(--color-action-soft) 46%, var(--color-surface));
  padding: var(--space-3) var(--subscription-account-refresh-inline);
}
.subscription-account__refresh-skeleton-header {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}
.subscription-account__refresh-skeleton-top {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}
.subscription-account__refresh-skeleton-top > div {
  display: flex;
  min-width: 0;
  gap: var(--space-2);
}
.subscription-account__refresh-skeleton-quotas {
  display: grid;
  gap: var(--space-2);
}
.subscription-account__refresh-skeleton-quota {
  display: grid;
  min-height: 34px;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
  overflow: hidden;
  border-radius: 6px;
  background: var(--color-surface-sunken);
  padding: 7px 10px;
}
.subscription-account__refresh-skeleton-quota > span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.subscription-account__refresh-skeleton-empty-quota,
.subscription-account__refresh-skeleton-hint {
  align-self: center;
}
.subscription-account__refresh-skeleton-credits {
  display: flex;
  min-height: var(--control-compact);
  align-items: center;
  gap: var(--space-2);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 4px 6px;
}
.subscription-account__refresh-skeleton-credits-expiry {
  margin-left: var(--space-1);
}
.subscription-account__refresh-skeleton-credits > :last-child {
  margin-left: auto;
  flex: none;
}
.subscription-account__refresh-skeleton-detail-control {
  display: grid;
  height: 44px;
  grid-template-columns: 1fr 44px 1fr;
  align-items: center;
  margin-top: 2px;
}
.subscription-account__refresh-skeleton-detail-control > span {
  height: 1px;
  background: var(--color-border-subtle);
}
.subscription-account__refresh-skeleton-detail {
  display: grid;
  gap: 13px;
  margin: calc(-1 * var(--space-3)) calc(-1 * var(--subscription-account-refresh-inline));
  border-top: 1px solid color-mix(in srgb, var(--color-action) 18%, var(--color-border-subtle));
  background: color-mix(in srgb, var(--color-action-soft) 72%, var(--color-surface));
  padding: 14px 18px 16px;
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
.subscription-account--disabled {
  border-color: var(--color-border-control);
  border-left-color: var(--color-neutral);
  box-shadow: none;
}
.subscription-account--disabled .subscription-account__status {
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--color-neutral) 28%, transparent);
}
.subscription-account--disabled .subscription-account__quota {
  --quota-accent: var(--color-neutral);
  --quota-tint: var(--color-surface-sunken);
}
.subscription-account__main {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4) 0;
}
.subscription-account__top {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}
.subscription-account__top-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}
.subscription-account__select {
  position: relative;
  display: grid;
  width: 20px;
  height: 20px;
  flex: none;
  align-items: center;
  justify-items: start;
  border: 0;
  background: transparent;
  padding: 0;
  cursor: pointer;
}
.subscription-account__select input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
}
.subscription-account__select-box {
  display: grid;
  width: 20px;
  height: 20px;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--color-text-faint) 42%, transparent);
  border-radius: 3px;
  background: var(--color-surface);
  color: var(--color-action);
}
.subscription-account__select input:checked + .subscription-account__select-box {
  border-color: color-mix(in srgb, var(--color-action) 60%, transparent);
}
.subscription-account__select input:focus-visible + .subscription-account__select-box {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
.subscription-account__select:has(input:disabled) {
  cursor: not-allowed;
}
.subscription-account__select:has(input:disabled) .subscription-account__select-box {
  opacity: 0.5;
}
.subscription-account__badges {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}
.subscription-account__mail {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  margin-right: 2px;
  font-family: var(--font-mono);
  font-size: var(--text-body);
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-account__sync-age {
  flex: none;
  margin-right: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}
.subscription-account__plan {
  display: inline-flex;
  min-height: 24px;
  flex: none;
  align-items: center;
  gap: 5px;
  border-radius: var(--radius-tag);
  background: var(--color-neutral-bg);
  color: var(--color-neutral);
  padding: 3px 8px;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: 600;
  line-height: 1;
  white-space: nowrap;
}
.subscription-account__plan--free {
  background: var(--color-neutral-bg);
  color: var(--color-neutral);
}
.subscription-account__plan--standard {
  background: var(--color-success-bg);
  color: var(--color-success);
}
.subscription-account__plan--premium {
  background: var(--color-action-soft);
  color: var(--color-action);
}
.subscription-account__plan--elite {
  background: var(--color-warning-bg);
  color: var(--color-warning);
}
.subscription-account__plan-icon {
  width: 14px;
  height: 14px;
  color: var(--color-text);
  font-size: 14px;
}
.subscription-account__status {
  flex: none;
  white-space: nowrap;
}
.subscription-account__actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 0;
  margin-left: auto;
}
.subscription-account__spacer {
  flex: 1 1 auto;
}
.subscription-account__quotas {
  display: grid;
  gap: var(--space-2);
}
.subscription-account__quota {
  /* 强调色用于左竖条与行底进度线，单值同时适配明暗；淡底只做状态提示 */
  --quota-accent: var(--color-border-control);
  --quota-tint: var(--color-surface-sunken);
  position: relative;
  display: grid;
  min-height: 34px;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
  overflow: hidden;
  border-radius: 6px;
  background: var(--quota-tint);
  /* 左竖条用 inset 阴影而非 border：border 会把 inset:0 的进度线整体右推 3px，
     导致左下圆角处出现断口。阴影不占盒模型，细线可贯通到最左侧与竖条重叠。 */
  box-shadow: inset 3px 0 0 var(--quota-accent);
  padding: 7px 10px 7px 13px;
}
.subscription-account__quota--success {
  --quota-accent: oklch(70% 0.16 158);
  --quota-tint: light-dark(#dcfeea, #112b21);
}
.subscription-account__quota--warning {
  --quota-accent: oklch(75% 0.152 75);
  --quota-tint: light-dark(#fff2e2, #302212);
}
.subscription-account__quota--danger {
  --quota-accent: oklch(65% 0.2 22);
  --quota-tint: light-dark(#fef0f0, #371a1d);
}
.subscription-account__quota-meter {
  position: absolute;
  z-index: 0;
  inset: 0;
  overflow: hidden;
  border-radius: inherit;
  pointer-events: none;
}
.subscription-account--disabled .subscription-account__quota-meter {
  filter: grayscale(1);
  opacity: 0.58;
}
.subscription-account__quota-fill {
  position: absolute;
  inset: auto auto 0 0;
  height: 3px;
  background: var(--quota-accent);
  transition: width var(--duration-fast) var(--easing-standard);
}
.subscription-account__quota-name {
  position: relative;
  z-index: 1;
  min-width: 0;
  overflow: hidden;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.subscription-account__quota-meta {
  position: relative;
  z-index: 1;
  display: inline-flex;
  min-width: max-content;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.subscription-account__quota-meta strong {
  color: var(--color-text);
  font-family: var(--font-mono);
  font-weight: 650;
}
.subscription-account__quota-reset {
  display: inline-flex;
  align-items: center;
  color: var(--color-text-muted);
  white-space: nowrap;
}
.subscription-account__quota-reset-time {
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
}
.subscription-account__quota-reset-prefix {
  margin-right: 0.3em;
}
.subscription-account__quota-reset-pending {
  cursor: help;
  text-decoration: underline dotted;
  text-decoration-color: var(--color-border-control);
  text-underline-offset: 3px;
}
.subscription-account__quota-reset-pending:focus-visible {
  border-radius: 3px;
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
.subscription-account__initial-sync {
  display: flex;
  min-height: 54px;
  align-items: center;
  gap: var(--space-2);
  border-radius: 6px;
  background: color-mix(in srgb, var(--color-action-soft) 72%, var(--color-surface));
  box-shadow: inset 3px 0 0 color-mix(in srgb, var(--color-action) 58%, transparent);
  padding: 8px 10px 8px 13px;
}
.subscription-account__initial-sync-icon {
  display: grid;
  width: 30px;
  height: 30px;
  flex: none;
  place-items: center;
  border-radius: 50%;
  background: var(--color-surface);
  color: var(--color-action);
  box-shadow: 0 1px 2px color-mix(in srgb, var(--color-action) 12%, transparent);
}
.subscription-account__initial-sync-copy {
  display: grid;
  min-width: 0;
  flex: 1 1 auto;
  gap: 2px;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  line-height: 1.35;
}
.subscription-account__initial-sync-copy strong {
  color: var(--color-text);
  font-weight: 650;
}
.subscription-account__initial-sync-action {
  flex: none;
}
.subscription-account__faint,
.subscription-account__hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.subscription-account__credits {
  display: flex;
  min-height: var(--control-compact);
  align-items: center;
  gap: var(--space-2);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 4px 6px;
  font-size: var(--text-sm);
}
.subscription-account__credits :deep(.subscription-account__credits-action) {
  width: 26px;
  min-height: 26px;
  padding: 0;
}
.subscription-account__credits > span:first-child,
.subscription-account__credits-expiry {
  color: var(--color-text-faint);
}
.subscription-account__credits-summary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border-radius: var(--radius-tag);
  padding: 3px 5px;
  cursor: help;
  transition: background-color var(--duration-fast) var(--easing-standard);
}
.subscription-account__credits-summary:hover {
  background: var(--color-interactive-hover);
}
.subscription-account__credits-summary:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
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
.subscription-account__credits-dots .subscription-account__credits-dot--warning {
  background: var(--color-warning);
}
.subscription-account__credits-dots .subscription-account__credits-dot--danger {
  background: var(--color-danger);
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
  --subscription-detail-overview-height: 92px;
}
.subscription-account__skeleton,
.subscription-account__detail-content {
  display: grid;
  gap: 13px;
}
.subscription-account__panels {
  display: grid;
  gap: 13px;
  margin-top: 13px;
}
.subscription-account__weight-chip {
  display: inline-flex;
  min-height: 24px;
  align-items: center;
  gap: 4px;
  border: 0;
  border-radius: var(--radius-tag);
  background: var(--color-info-bg);
  color: var(--color-info);
  padding: 3px 7px 3px 6px;
  font: inherit;
  font-size: var(--text-sm);
  font-weight: 650;
  white-space: nowrap;
  cursor: pointer;
}
.subscription-account__weight-chip:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.subscription-account__weight-chip:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
.subscription-account__weight-chip > b {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 700;
}
.subscription-account__weight-mode {
  flex: none;
}
.subscription-account__weight-input {
  width: 64px;
  min-height: 26px;
  flex: none;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 6px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
}
.subscription-account__weight-input.is-concealed {
  visibility: hidden;
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
.subscription-account__models {
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
/* 双列列表接近最小卡片宽度时，操作不能被右侧裁切；同步时间与操作组整体换行。 */
@container (max-width: 480px) {
  .subscription-account__top > .subscription-account__top-row:first-child {
    flex-wrap: wrap;
  }
  .subscription-account__top
    > .subscription-account__top-row:first-child
    .subscription-account__actions {
    width: 100%;
    justify-content: flex-end;
  }
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
  .subscription-account__actions {
    margin-left: auto;
  }
  .subscription-account__refresh-skeleton {
    --subscription-account-refresh-inline: 14px;
  }
  .subscription-account__skeleton {
    --subscription-detail-overview-height: 154px;
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
  .subscription-account__quota-fill {
    transition: none;
  }
  .subscription-account__detail-spinner,
  .subscription-account__sync-icon--spinning {
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
