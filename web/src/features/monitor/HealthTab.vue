<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { healthQueryOptions, type HealthProblemCredentialDto } from '@/app/resources/health'
import { monitorLocation } from '@/app/route-locations'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import { formatLocalInstant } from '@/lib/format'

import GroupHealthCollection from './GroupHealthCollection.vue'
import HealthProblemCollection from './HealthProblemCollection.vue'
import HealthSummaryStrip from './HealthSummaryStrip.vue'
import RequestLogHealthCard from './RequestLogHealthCard.vue'
import { healthMonitorQuery, parseHealthMonitorState } from './monitor-route'

interface ProblemItem {
  credential: HealthProblemCredentialDto
  kind: 'cooldown' | 'blacklisted'
  tone: 'warning' | 'danger'
}

interface RecoveryDisplay {
  relative: string
  exact: string
}

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const healthQuery = useQuery(healthQueryOptions(client))
const initialLoading = useStableLoading(() => healthQuery.isPending.value)
const healthRefreshing = computed(
  () => healthQuery.data.value !== undefined && healthQuery.isFetching.value,
)

const isVisible = ref(document.visibilityState !== 'hidden')
const elapsedMs = ref(0)
const groupsExpanded = computed(() => parseHealthMonitorState(route.query).groupsExpanded)
let elapsedTimer: ReturnType<typeof setInterval> | undefined
let elapsedStartedAt = 0

const hasStaleData = computed(
  () => healthQuery.isError.value && healthQuery.data.value !== undefined,
)
const timerShouldRun = computed(
  () => isVisible.value && healthQuery.data.value !== undefined && !healthQuery.isError.value,
)
const problemItems = computed<ProblemItem[]>(() => {
  const data = healthQuery.data.value
  if (!data) return []
  const groupCounts = new Map(data.groups.map((group) => [group.id, group.counts]))
  const items: ProblemItem[] = [
    ...data.cooldown_credentials.map((credential) => ({
      credential,
      kind: 'cooldown' as const,
      tone: 'warning' as const,
    })),
    ...data.blacklisted_credentials.map((credential) => ({
      credential,
      kind: 'blacklisted' as const,
      tone: 'danger' as const,
    })),
  ]

  return items.sort((left, right) => {
    const leftUnavailable = groupCounts.get(left.credential.group_id)?.available === 0 ? 0 : 1
    const rightUnavailable = groupCounts.get(right.credential.group_id)?.available === 0 ? 0 : 1
    const leftKind = left.kind === 'blacklisted' ? 0 : 1
    const rightKind = right.kind === 'blacklisted' ? 0 : 1
    const leftRecovery = left.credential.cooldown_until_ms ?? Number.MAX_SAFE_INTEGER
    const rightRecovery = right.credential.cooldown_until_ms ?? Number.MAX_SAFE_INTEGER
    return (
      leftUnavailable - rightUnavailable ||
      leftKind - rightKind ||
      leftRecovery - rightRecovery ||
      left.credential.group_name.localeCompare(right.credential.group_name) ||
      left.credential.credential_id - right.credential.credential_id
    )
  })
})
const focusContentHeight = computed(() => {
  const rowHeight = 76
  const headerHeight = 38
  const compactMinimum = 196
  const visibleRows = Math.min(problemItems.value.length, 3)
  return Math.max(compactMinimum, headerHeight + visibleRows * rowHeight)
})
const recoveryByCredential = computed<Record<number, RecoveryDisplay | undefined>>(() =>
  Object.fromEntries(
    problemItems.value.map((item) => [
      item.credential.credential_id,
      recoveryDisplay(item.credential),
    ]),
  ),
)
const earliestCooldown = computed<RecoveryDisplay | null>(() => {
  const earliest = problemItems.value
    .filter((item) => item.kind === 'cooldown' && item.credential.cooldown_until_ms !== null)
    .sort(
      (left, right) =>
        (left.credential.cooldown_until_ms ?? Number.MAX_SAFE_INTEGER) -
        (right.credential.cooldown_until_ms ?? Number.MAX_SAFE_INTEGER),
    )[0]
  return earliest ? (recoveryByCredential.value[earliest.credential.credential_id] ?? null) : null
})

function stopElapsedTimer(): void {
  if (elapsedTimer === undefined) return
  clearInterval(elapsedTimer)
  elapsedTimer = undefined
}

function syncElapsedTimer(): void {
  stopElapsedTimer()
  if (!timerShouldRun.value) return
  elapsedMs.value = Math.max(0, performance.now() - elapsedStartedAt)
  elapsedTimer = setInterval(() => {
    elapsedMs.value = Math.max(0, performance.now() - elapsedStartedAt)
  }, 1_000)
}

watch(
  () => healthQuery.dataUpdatedAt.value,
  (updatedAt, previousUpdatedAt) => {
    if (updatedAt > 0 && updatedAt !== previousUpdatedAt) {
      const initialElapsedMs = Math.max(0, Date.now() - updatedAt)
      elapsedStartedAt = performance.now() - initialElapsedMs
      elapsedMs.value = initialElapsedMs
    }
    syncElapsedTimer()
  },
  { immediate: true },
)
watch([isVisible, () => healthQuery.isError.value], syncElapsedTimer)

function handleVisibilityChange(): void {
  isVisible.value = document.visibilityState !== 'hidden'
}

onMounted(() => document.addEventListener('visibilitychange', handleVisibilityChange))
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  stopElapsedTimer()
})

function remainingLabel(totalSeconds: number): string {
  if (totalSeconds >= 3_600) {
    return t('monitor.health.recovery.hoursMinutes', {
      hours: Math.floor(totalSeconds / 3_600),
      minutes: Math.floor((totalSeconds % 3_600) / 60),
    })
  }
  if (totalSeconds >= 60) {
    return t('monitor.health.recovery.minutesSeconds', {
      minutes: Math.floor(totalSeconds / 60),
      seconds: totalSeconds % 60,
    })
  }
  return t('monitor.health.recovery.seconds', { seconds: totalSeconds })
}

function recoveryDisplay(credential: HealthProblemCredentialDto): RecoveryDisplay | undefined {
  const observedAtMS = healthQuery.data.value?.observed_at_ms
  if (credential.cooldown_until_ms === null || observedAtMS === undefined) return undefined
  const remainingSeconds = Math.max(
    0,
    Math.ceil((credential.cooldown_until_ms - (observedAtMS + elapsedMs.value)) / 1_000),
  )
  return {
    relative: remainingLabel(remainingSeconds),
    exact: t('monitor.health.recovery.exact', {
      time: formatLocalInstant(credential.cooldown_until_ms, locale.value),
    }),
  }
}

async function refresh(): Promise<void> {
  await healthQuery.refetch()
}

function toggleGroups(): void {
  void router.push(monitorLocation(healthMonitorQuery({ groupsExpanded: !groupsExpanded.value })))
}

defineExpose({ refresh })
</script>

<template>
  <div class="health-tab" :aria-busy="healthQuery.isFetching.value ? 'true' : undefined">
    <AsyncRefreshIndicator :active="healthRefreshing" :label="t('monitor.health.loading')" />

    <SkeletonSurface
      v-if="healthQuery.isPending.value || initialLoading"
      variant="dashboard"
      :concealed="!initialLoading"
      :label="t('monitor.health.loading')"
    />

    <QueryFeedback
      v-else-if="healthQuery.isError.value && !healthQuery.data.value"
      state="error"
      :message="t('monitor.health.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="healthQuery.refetch()"
    />

    <template v-else-if="healthQuery.data.value">
      <QueryFeedback
        v-if="hasStaleData"
        state="stale"
        :message="t('monitor.health.stale')"
        :retry-label="t('common.retry')"
        @retry="healthQuery.refetch()"
      />

      <HealthSummaryStrip
        :counts="healthQuery.data.value.counts"
        :earliest-cooldown="earliestCooldown"
      />

      <div
        class="health-focus-grid"
        :style="{ '--health-focus-content-height': `${focusContentHeight}px` }"
      >
        <HealthProblemCollection
          :items="problemItems"
          :recovery-by-credential="recoveryByCredential"
          :stats-window-seconds="healthQuery.data.value.stats_window_seconds"
          :available-count="healthQuery.data.value.counts.available"
        />
        <RequestLogHealthCard :stats="healthQuery.data.value.request_log" />
      </div>

      <GroupHealthCollection
        :groups="healthQuery.data.value.groups"
        :expanded="groupsExpanded"
        @toggle="toggleGroups"
      />
    </template>
  </div>
</template>

<style scoped>
.health-tab {
  display: grid;
  min-width: 0;
  gap: var(--space-6);
}

.health-focus-grid {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 2fr) minmax(320px, 0.82fr);
  align-items: start;
  gap: 20px;
}

.health-tab :deep(.query-feedback--error > span) {
  color: var(--color-text);
}

@media (max-width: 1099px) {
  .health-focus-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
