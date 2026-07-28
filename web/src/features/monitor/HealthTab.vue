<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { ChevronDown } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  healthQueryOptions,
  type HealthProblemKeyDto,
  type KeyCounts,
} from '@/app/resources/health'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

const client = useApiClient()
const { t } = useI18n()

const healthQuery = useQuery(healthQueryOptions(client, true))

const isVisible = ref(document.visibilityState !== 'hidden')
const elapsedMs = ref(0)
const expandedKeyIds = ref(new Set<number>())
let elapsedTimer: ReturnType<typeof setInterval> | undefined
let elapsedStartedAt = 0

const hasStaleData = computed(
  () => healthQuery.isError.value && healthQuery.data.value !== undefined,
)
const timerShouldRun = computed(
  () => isVisible.value && healthQuery.data.value !== undefined && !healthQuery.isError.value,
)
const problemSections = computed(() => [
  {
    kind: 'cooldown',
    label: t('monitor.health.problems.cooldown'),
    tone: 'warning' as const,
    keys: healthQuery.data.value?.cooldown_keys ?? [],
  },
  {
    kind: 'blacklisted',
    label: t('monitor.health.problems.blacklisted'),
    tone: 'danger' as const,
    keys: healthQuery.data.value?.blacklisted_keys ?? [],
  },
])
const requestLogCounters = computed(() => {
  const requestLog = healthQuery.data.value?.request_log
  if (!requestLog) return []
  return [
    {
      label: t('monitor.health.requestLog.enqueued'),
      value: requestLog.enqueued_total,
      testId: 'request-log-enqueued',
    },
    {
      label: t('monitor.health.requestLog.persisted'),
      value: requestLog.persisted_total,
      testId: 'request-log-persisted',
    },
    {
      label: t('monitor.health.requestLog.droppedNotRunning'),
      value: requestLog.dropped_not_running_total,
      testId: 'request-log-dropped-not-running',
    },
    {
      label: t('monitor.health.requestLog.droppedQueueFull'),
      value: requestLog.dropped_queue_full_total,
      testId: 'request-log-dropped-queue-full',
    },
    {
      label: t('monitor.health.requestLog.droppedStopping'),
      value: requestLog.dropped_stopping_total,
      testId: 'request-log-dropped-stopping',
    },
    {
      label: t('monitor.health.requestLog.droppedPersistFailed'),
      value: requestLog.dropped_persist_failed_total,
      testId: 'request-log-dropped-persist-failed',
    },
    {
      label: t('monitor.health.requestLog.droppedShutdown'),
      value: requestLog.dropped_shutdown_total,
      testId: 'request-log-dropped-shutdown',
    },
    {
      label: t('monitor.health.requestLog.droppedTotal'),
      value: requestLog.dropped_total,
      testId: 'request-log-dropped-total',
    },
    {
      label: t('monitor.health.requestLog.writeFailures'),
      value: requestLog.write_failure_total,
      testId: 'request-log-write-failures',
    },
    {
      label: t('monitor.health.requestLog.retentionFailures'),
      value: requestLog.retention_delete_failure_total,
      testId: 'request-log-retention-failures',
    },
    {
      label: t('monitor.health.requestLog.queueDepth'),
      value: requestLog.queue_depth,
      testId: 'request-log-queue-depth',
    },
    {
      label: t('monitor.health.requestLog.queueCapacity'),
      value: requestLog.queue_capacity,
      testId: 'request-log-queue-capacity',
    },
  ]
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
  const wasVisible = isVisible.value
  isVisible.value = document.visibilityState !== 'hidden'
  if (!wasVisible && isVisible.value) void healthQuery.refetch()
}

onMounted(() => document.addEventListener('visibilitychange', handleVisibilityChange))
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  stopElapsedTimer()
})

function summaryTone(counts: KeyCounts): 'success' | 'warning' | 'danger' | 'neutral' {
  if (counts.total === 0) return 'neutral'
  if (counts.available === 0) return 'danger'
  if (counts.cooldown > 0 || counts.blacklisted > 0) return 'warning'
  return 'success'
}

function summaryLabel(counts: KeyCounts): string {
  if (counts.total === 0) return t('monitor.health.status.empty')
  if (counts.available === 0) return t('monitor.health.status.unavailable')
  if (counts.cooldown > 0 || counts.blacklisted > 0) {
    return t('monitor.health.status.exceptions')
  }
  return t('monitor.health.status.clear')
}

function groupStatusLabel(enabled: boolean, counts: KeyCounts): string {
  if (!enabled) return t('monitor.health.groups.disabled')
  if (counts.total === 0) return t('monitor.health.groups.emptyKeys')
  if (counts.available === 0) return t('monitor.health.groups.unavailable')
  if (counts.cooldown > 0 || counts.blacklisted > 0) {
    return t('monitor.health.groups.attention')
  }
  return t('monitor.health.groups.available')
}

function groupStatusTone(
  enabled: boolean,
  counts: KeyCounts,
): 'success' | 'warning' | 'danger' | 'neutral' {
  if (!enabled) return 'neutral'
  if (counts.total === 0) return 'neutral'
  if (counts.available === 0) return 'danger'
  if (counts.cooldown > 0 || counts.blacklisted > 0) return 'warning'
  return 'success'
}

function isExpanded(keyId: number): boolean {
  return expandedKeyIds.value.has(keyId)
}

function toggleExpanded(keyId: number): void {
  const next = new Set(expandedKeyIds.value)
  if (next.has(keyId)) next.delete(keyId)
  else next.add(keyId)
  expandedKeyIds.value = next
}

function remainingTime(key: HealthProblemKeyDto): string {
  const observedAt = healthQuery.data.value?.observed_at
  if (!key.cooldown_until || !observedAt) return ''
  const expiryMs = Date.parse(key.cooldown_until)
  const observedMs = Date.parse(observedAt)
  if (!Number.isFinite(expiryMs) || !Number.isFinite(observedMs)) return ''
  const remainingSeconds = Math.max(
    0,
    Math.ceil((expiryMs - (observedMs + elapsedMs.value)) / 1_000),
  )
  const minutes = Math.floor(remainingSeconds / 60)
  const seconds = String(remainingSeconds % 60).padStart(2, '0')
  return `${minutes}:${seconds}`
}

function recoveryModeLabel(mode: string): string {
  if (mode === 'cooldown_expiry') return t('monitor.health.recovery.cooldownExpiry')
  if (mode === 'validation_probe') return t('monitor.health.recovery.validationProbe')
  return t('monitor.health.recovery.unknown')
}
</script>

<template>
  <div class="health-tab">
    <QueryFeedback
      v-if="healthQuery.isPending.value"
      state="loading"
      :message="t('monitor.health.loading')"
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

      <SurfaceCard class="health-card health-summary">
        <header class="health-card__heading">
          <div>
            <h2>{{ t('monitor.health.summary.title') }}</h2>
            <p>{{ t('monitor.health.summary.description') }}</p>
          </div>
          <StatusBadge :tone="summaryTone(healthQuery.data.value.counts)">
            {{ summaryLabel(healthQuery.data.value.counts) }}
          </StatusBadge>
        </header>

        <div class="health-meta">
          <span>{{
            t('monitor.health.summary.revision', {
              revision: healthQuery.data.value.snapshot_revision,
            })
          }}</span>
          <span>{{
            t('monitor.health.summary.observedAt', {
              time: healthQuery.data.value.observed_at,
            })
          }}</span>
        </div>

        <div class="count-grid">
          <span>{{
            t('monitor.health.counts.total', { total: healthQuery.data.value.counts.total })
          }}</span>
          <span>{{
            t('monitor.health.counts.available', {
              available: healthQuery.data.value.counts.available,
            })
          }}</span>
          <span>{{
            t('monitor.health.counts.cooldown', {
              cooldown: healthQuery.data.value.counts.cooldown,
            })
          }}</span>
          <span>{{
            t('monitor.health.counts.blacklisted', {
              blacklisted: healthQuery.data.value.counts.blacklisted,
            })
          }}</span>
          <span>{{
            t('monitor.health.counts.disabled', {
              disabled: healthQuery.data.value.counts.disabled,
            })
          }}</span>
        </div>
      </SurfaceCard>

      <section class="health-section" aria-labelledby="health-groups-heading">
        <header class="health-section__heading">
          <div>
            <h2 id="health-groups-heading">{{ t('monitor.health.groups.title') }}</h2>
            <p>{{ t('monitor.health.groups.description') }}</p>
          </div>
        </header>

        <p v-if="healthQuery.data.value.groups.length === 0" class="health-empty">
          {{ t('monitor.health.groups.empty') }}
        </p>
        <div v-else class="group-health-grid">
          <SurfaceCard
            v-for="group in healthQuery.data.value.groups"
            :key="group.id"
            class="health-card group-health-card"
          >
            <header class="health-card__heading">
              <RouterLink class="group-link" :to="`/groups/${group.id}`">
                {{ group.name }} · #{{ group.id }}
              </RouterLink>
              <StatusBadge :tone="groupStatusTone(group.enabled, group.counts)">
                {{ groupStatusLabel(group.enabled, group.counts) }}
              </StatusBadge>
            </header>
            <div class="count-grid count-grid--compact">
              <span>{{ t('monitor.health.counts.total', { total: group.counts.total }) }}</span>
              <span>{{
                t('monitor.health.counts.available', { available: group.counts.available })
              }}</span>
              <span>{{
                t('monitor.health.counts.cooldown', { cooldown: group.counts.cooldown })
              }}</span>
              <span>{{
                t('monitor.health.counts.blacklisted', {
                  blacklisted: group.counts.blacklisted,
                })
              }}</span>
              <span>{{
                t('monitor.health.counts.disabled', { disabled: group.counts.disabled })
              }}</span>
            </div>
          </SurfaceCard>
        </div>
      </section>

      <section class="health-section" aria-labelledby="health-problems-heading">
        <header class="health-section__heading">
          <div>
            <h2 id="health-problems-heading">{{ t('monitor.health.problems.title') }}</h2>
            <p>{{ t('monitor.health.problems.description') }}</p>
          </div>
        </header>

        <p
          v-if="
            healthQuery.data.value.cooldown_keys.length === 0 &&
            healthQuery.data.value.blacklisted_keys.length === 0
          "
          class="health-empty"
        >
          {{ t('monitor.health.problems.empty') }}
        </p>
        <div v-else class="problem-sections">
          <section v-for="section in problemSections" :key="section.kind" class="problem-section">
            <h3>{{ section.label }}</h3>
            <p v-if="section.keys.length === 0" class="health-empty">
              {{ t('monitor.health.problems.noneForStatus') }}
            </p>
            <article
              v-for="key in section.keys"
              :key="key.key_id"
              class="problem-key"
              :data-key-id="key.key_id"
            >
              <button
                type="button"
                class="problem-key__toggle"
                :data-test="`problem-key-${key.key_id}`"
                :aria-expanded="isExpanded(key.key_id)"
                :aria-controls="`problem-key-details-${key.key_id}`"
                @click="toggleExpanded(key.key_id)"
              >
                <span class="problem-key__identity">
                  {{ t('monitor.health.problems.keyId', { id: key.key_id }) }}
                </span>
                <ChevronDown
                  class="problem-key__chevron"
                  :class="{ 'problem-key__chevron--expanded': isExpanded(key.key_id) }"
                  :size="18"
                  aria-hidden="true"
                />
              </button>

              <div class="problem-key__summary">
                <RouterLink class="group-link" :to="`/groups/${key.group_id}?tab=keys`">
                  {{ key.group_name }} · #{{ key.group_id }}
                </RouterLink>
                <StatusBadge :tone="section.tone">{{ section.label }}</StatusBadge>
                <span
                  v-if="key.cooldown_until"
                  class="problem-key__remaining"
                  :data-test="`remaining-${key.key_id}`"
                >
                  {{
                    t('monitor.health.problems.remaining', {
                      time: remainingTime(key),
                    })
                  }}
                </span>
              </div>

              <div
                v-if="isExpanded(key.key_id)"
                :id="`problem-key-details-${key.key_id}`"
                class="problem-key__details"
                :data-test="`problem-key-details-${key.key_id}`"
              >
                <dl class="detail-grid">
                  <div>
                    <dt>{{ t('monitor.health.details.failureCount') }}</dt>
                    <dd :data-test="`failure-count-${key.key_id}`">{{ key.failure_count }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.health.details.recentSuccessCount') }}</dt>
                    <dd>{{ key.recent_success_count }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.health.details.recentFailureCount') }}</dt>
                    <dd>{{ key.recent_failure_count }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.health.details.consecutiveFailureCount') }}</dt>
                    <dd>{{ key.consecutive_failure_count }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.health.details.manualWeight') }}</dt>
                    <dd>{{ key.weight_manual ?? t('monitor.health.details.automatic') }}</dd>
                  </div>
                  <div>
                    <dt>{{ t('monitor.health.details.autoWeight') }}</dt>
                    <dd :data-test="`auto-weight-${key.key_id}`">{{ key.weight_auto }}</dd>
                  </div>
                </dl>
                <div class="recovery-facts">
                  <span>{{
                    key.recovery.automatic
                      ? t('monitor.health.recovery.automatic')
                      : t('monitor.health.recovery.notAutomatic')
                  }}</span>
                  <span>{{ recoveryModeLabel(key.recovery.mode) }}</span>
                  <time v-if="key.recovery.at" :datetime="key.recovery.at">
                    {{ key.recovery.at }}
                  </time>
                  <span v-else-if="key.recovery.mode === 'validation_probe'">
                    {{ t('monitor.health.recovery.runtimeDecides') }}
                  </span>
                </div>
              </div>
            </article>
          </section>
        </div>
      </section>

      <SurfaceCard class="health-card request-log-health">
        <header class="health-card__heading">
          <div>
            <h2>{{ t('monitor.health.requestLog.title') }}</h2>
            <p>{{ t('monitor.health.requestLog.description') }}</p>
          </div>
        </header>

        <dl class="request-log-grid">
          <div v-for="counter in requestLogCounters" :key="counter.testId">
            <dt>{{ counter.label }}</dt>
            <dd :data-test="counter.testId">{{ counter.value }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.health.requestLog.lastWriteFailureAt') }}</dt>
            <dd>
              <time
                v-if="healthQuery.data.value.request_log.last_write_failure_at"
                :datetime="healthQuery.data.value.request_log.last_write_failure_at"
              >
                {{ healthQuery.data.value.request_log.last_write_failure_at }}
              </time>
              <span v-else>{{ t('monitor.health.none') }}</span>
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.health.requestLog.lastRetentionFailureAt') }}</dt>
            <dd>
              <time
                v-if="healthQuery.data.value.request_log.last_retention_failure_at"
                :datetime="healthQuery.data.value.request_log.last_retention_failure_at"
              >
                {{ healthQuery.data.value.request_log.last_retention_failure_at }}
              </time>
              <span v-else>{{ t('monitor.health.none') }}</span>
            </dd>
          </div>
        </dl>
      </SurfaceCard>
    </template>
  </div>
</template>

<style scoped>
.health-tab,
.health-section,
.problem-sections {
  display: grid;
  gap: var(--space-5);
}

.health-card {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
  padding: var(--space-6);
}

.health-card__heading,
.health-section__heading,
.problem-key__summary {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}

.health-card__heading > div,
.health-section__heading > div {
  min-width: 0;
}

.health-card__heading h2,
.health-section__heading h2,
.problem-section h3 {
  margin: 0;
  font-size: 1rem;
}

.health-card__heading p,
.health-section__heading p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}

.health-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.8125rem;
}

.health-meta span,
.count-grid span,
.recovery-facts span,
.recovery-facts time {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding: 6px 10px;
}

.count-grid {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: var(--space-2);
}

.count-grid span {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  text-align: center;
}

.group-health-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr));
  gap: var(--space-4);
}

.group-link {
  display: inline-flex;
  min-width: 44px;
  max-width: 100%;
  min-height: 44px;
  align-items: center;
  color: var(--color-primary);
  font-weight: 700;
  overflow-wrap: anywhere;
  text-decoration: underline;
  text-decoration-color: transparent;
  text-underline-offset: 3px;
}

.group-link:hover {
  text-decoration-color: currentColor;
}

.health-empty {
  margin: 0;
  border: 1px dashed var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text-muted);
  padding: var(--space-4);
}

.problem-section {
  display: grid;
  gap: var(--space-3);
}

.problem-key {
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  background: var(--color-surface);
}

.problem-key__toggle {
  display: flex;
  width: 100%;
  min-height: 44px;
  align-items: center;
  justify-content: space-between;
  border: 0;
  background: transparent;
  color: var(--color-text);
  padding: var(--space-3) var(--space-4);
  cursor: pointer;
}

.problem-key__toggle > * {
  min-width: 0;
}

.problem-key__identity,
.problem-key__remaining,
.detail-grid dd,
.request-log-grid dd {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.problem-key__identity {
  font-weight: 700;
}

.problem-key__chevron {
  transition: transform var(--duration-fast) ease;
}

.problem-key__chevron--expanded {
  transform: rotate(180deg);
}

.problem-key__summary {
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-start;
  border-top: 1px solid var(--color-border);
  padding: var(--space-3) var(--space-4);
}

.problem-key__remaining {
  margin-left: auto;
}

.problem-key__details {
  display: grid;
  gap: var(--space-4);
  border-top: 1px solid var(--color-border);
  background: var(--color-surface-secondary);
  padding: var(--space-4);
}

.detail-grid,
.request-log-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.detail-grid div,
.request-log-grid div {
  min-width: 0;
}

.detail-grid dt,
.request-log-grid dt {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.detail-grid dd,
.request-log-grid dd {
  margin: var(--space-1) 0 0;
  overflow-wrap: anywhere;
  font-weight: 700;
}

.recovery-facts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.health-tab :deep(.query-feedback--error > span) {
  color: var(--color-text);
}

@media (max-width: 760px) {
  .health-card__heading,
  .problem-key__summary {
    align-items: flex-start;
    flex-direction: column;
  }

  .count-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .count-grid span {
    text-align: left;
  }

  .problem-key__remaining {
    margin-left: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .problem-key__chevron {
    transition: none;
  }
}
</style>
