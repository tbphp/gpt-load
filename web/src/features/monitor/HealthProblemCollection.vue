<script setup lang="ts">
import { ArrowRight, ScrollText } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HealthProblemKeyDto } from '@/app/resources/health'
import { groupDetailLocation, monitorLocation } from '@/app/route-locations'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import MonitorSectionHeading from './MonitorSectionHeading.vue'

interface HealthProblemItem {
  key: HealthProblemKeyDto
  kind: 'cooldown' | 'blacklisted'
  tone: 'warning' | 'danger'
}

interface RecoveryDisplay {
  relative: string
  exact: string
}

const props = defineProps<{
  items: HealthProblemItem[]
  recoveryByKey: Record<number, RecoveryDisplay | undefined>
  statsWindowSeconds: number
  availableCount: number
}>()
const { n, t } = useI18n()
const statsWindowMinutes = computed(() => Math.max(1, Math.round(props.statsWindowSeconds / 60)))

function statusLabel(item: HealthProblemItem): string {
  return t(`monitor.health.problems.${item.kind}`)
}

function keyMeta(key: HealthProblemKeyDto): string {
  const result = [t('monitor.health.problems.keyMeta', { group: key.group_name })]
  if (key.last_status_code !== null) result.push(String(key.last_status_code))
  result.push(t(`monitor.health.failureCategories.${key.last_failure_category}`))
  return result.join(' · ')
}
</script>

<template>
  <section class="problem-health" aria-labelledby="problem-health-title">
    <MonitorSectionHeading
      id="problem-health-title"
      :title="t('monitor.health.problems.title')"
      :description="
        t('monitor.health.problems.description', {
          minutes: n(statsWindowMinutes),
        })
      "
      :meta="
        items.length > 0
          ? t('monitor.health.problems.count', { count: n(items.length) })
          : undefined
      "
    />

    <div
      v-if="items.length === 0"
      class="problem-health__clear-panel"
      :class="{ 'problem-health__clear-panel--inactive': availableCount === 0 }"
      role="status"
      :aria-label="t('monitor.health.problems.empty')"
    >
      <div class="problem-health__clear-main">
        <StatusBadge :tone="availableCount > 0 ? 'success' : 'neutral'" size="compact">
          {{
            availableCount > 0
              ? t('monitor.health.problems.empty')
              : t('monitor.health.problems.inactiveTitle')
          }}
        </StatusBadge>
        <div class="problem-health__clear-copy">
          <span>
            {{
              availableCount > 0
                ? t('monitor.health.problems.emptyDescription', {
                    count: n(availableCount),
                  })
                : t('monitor.health.problems.inactiveDescription')
            }}
          </span>
          <small>{{ t('monitor.health.problems.emptyHint') }}</small>
        </div>
      </div>
      <span class="problem-health__clear-meta">
        {{ t('monitor.health.problems.windowLabel', { minutes: n(statsWindowMinutes) }) }}
      </span>
    </div>

    <LedgerRecordList
      v-else
      :label="t('monitor.health.problems.tableLabel')"
      :row-count="items.length + 1"
      grid-class="problem-health-grid"
    >
      <template #header>
        <span role="columnheader">{{ t('monitor.health.problems.columns.identity') }}</span>
        <span role="columnheader">{{ t('monitor.health.problems.columns.status') }}</span>
        <span role="columnheader">{{ t('monitor.health.problems.columns.window') }}</span>
        <span role="columnheader">{{ t('monitor.health.problems.columns.recovery') }}</span>
        <span class="problem-health__actions-heading" role="columnheader">
          {{ t('monitor.health.problems.columns.actions') }}
        </span>
      </template>

      <template v-for="(item, index) in items" :key="item.key.key_id">
        <article
          class="ledger-record-list__record problem-health-record"
          role="row"
          :aria-rowindex="index + 2"
        >
          <div class="ledger-record-list__cell problem-health-record__identity" role="cell">
            <RouterLink
              class="problem-health-record__mask"
              :to="groupDetailLocation(item.key.group_id, { tab: 'keys' })"
            >
              {{ item.key.mask }}
            </RouterLink>
            <small>{{ keyMeta(item.key) }}</small>
          </div>

          <div class="ledger-record-list__cell" role="cell">
            <StatusBadge :tone="item.tone" size="compact">
              {{ statusLabel(item) }}
            </StatusBadge>
          </div>

          <div class="ledger-record-list__cell problem-health-record__window" role="cell">
            <span
              class="problem-health-record__window-summary"
              :aria-label="
                t('monitor.health.problems.window', {
                  minutes: n(statsWindowMinutes),
                  problems: n(item.key.recent_problem_count),
                  successes: n(item.key.recent_success_count),
                })
              "
            >
              <span>
                {{
                  t('monitor.health.problems.windowCompactPrefix', {
                    minutes: n(statsWindowMinutes),
                  })
                }}
              </span>
              <span
                :class="
                  item.key.recent_problem_count === 0
                    ? 'problem-health-record__window-value--success'
                    : 'problem-health-record__window-value--danger'
                "
              >
                {{ n(item.key.recent_problem_count) }}
              </span>
              <span aria-hidden="true">/</span>
              <span
                :class="
                  item.key.recent_success_count === 0
                    ? 'problem-health-record__window-value--danger'
                    : 'problem-health-record__window-value--success'
                "
              >
                {{ n(item.key.recent_success_count) }}
              </span>
            </span>
            <small>
              {{
                t('monitor.health.problems.consecutive', {
                  count: n(item.key.consecutive_problem_count),
                })
              }}
            </small>
          </div>

          <div class="ledger-record-list__cell problem-health-record__recovery" role="cell">
            <template v-if="item.kind === 'cooldown' && recoveryByKey[item.key.key_id]">
              <AppTooltip :content="recoveryByKey[item.key.key_id]!.exact" side="bottom">
                <span class="problem-health-record__recovery-time" tabindex="0">
                  {{
                    t('monitor.health.problems.cooldownRecovery', {
                      time: recoveryByKey[item.key.key_id]!.relative,
                    })
                  }}
                </span>
              </AppTooltip>
              <small>{{ t('monitor.health.problems.cooldownHint') }}</small>
            </template>
            <template v-else>
              <span>{{ t('monitor.health.problems.validationRecovery') }}</span>
              <small>{{ t('monitor.health.problems.validationHint') }}</small>
            </template>
          </div>

          <div class="ledger-record-list__cell problem-health-record__actions" role="cell">
            <RouterLink
              v-slot="{ navigate }"
              :to="monitorLocation({ tab: 'logs', access_key_id: String(item.key.key_id) })"
              custom
            >
              <IconButton
                role="link"
                variant="ghost"
                size="compact"
                :label="t('monitor.health.problems.viewLogs', { key: item.key.mask })"
                @click="navigate"
              >
                <ScrollText :size="15" aria-hidden="true" />
              </IconButton>
            </RouterLink>
            <RouterLink
              v-slot="{ navigate }"
              :to="groupDetailLocation(item.key.group_id, { tab: 'keys' })"
              custom
            >
              <IconButton
                role="link"
                variant="ghost"
                size="compact"
                :label="t('monitor.health.problems.viewGroup', { group: item.key.group_name })"
                @click="navigate"
              >
                <ArrowRight :size="15" aria-hidden="true" />
              </IconButton>
            </RouterLink>
          </div>
        </article>
      </template>
    </LedgerRecordList>
  </section>
</template>

<style scoped>
.problem-health {
  display: grid;
  min-width: 0;
  grid-template-rows: auto var(--health-focus-content-height, 266px);
  gap: var(--space-4);
}

.problem-health-grid {
  --ledger-record-list-grid: minmax(220px, 1.5fr) 90px minmax(170px, 1fr) minmax(165px, 1.05fr) 64px;
  --ledger-record-list-column-gap: 10px;
  height: 100%;
  align-content: start;
  overflow-y: auto;
  scrollbar-gutter: stable;
}

.problem-health-grid :deep(.ledger-record-list__header) {
  position: sticky;
  z-index: 1;
  top: 0;
  background: var(--color-surface);
}

.problem-health-record {
  --ledger-record-list-record-min-height: 76px;
  --ledger-record-list-record-padding: 10px 0;
}

.problem-health-record__identity,
.problem-health-record__window,
.problem-health-record__recovery {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  flex-direction: column;
  gap: var(--space-1);
}

.problem-health-record__mask {
  max-width: 100%;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-weight: 620;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.problem-health-record__mask:hover {
  color: var(--color-action);
}

.problem-health-record small {
  width: 100%;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  line-height: var(--line-normal);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.problem-health-record__window > span,
.problem-health-record__recovery > span {
  max-width: 100%;
  color: var(--color-text);
  font-size: var(--text-sm);
  line-height: var(--line-normal);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.problem-health-record__window > span {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.problem-health-record__window-summary {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
}

.problem-health-record__window-value--success {
  color: var(--color-success);
}

.problem-health-record__window-value--danger {
  color: var(--color-danger);
}

.problem-health-record__recovery-time {
  width: fit-content;
  border-radius: 3px;
}

.problem-health-record__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 2px;
}

.problem-health__actions-heading {
  text-align: right;
}

.problem-health__clear-panel {
  display: flex;
  min-width: 0;
  height: 100%;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-5);
  border-top: 1px solid var(--color-border-control);
  border-bottom: 1px solid var(--color-border-control);
  background: color-mix(in srgb, var(--color-success-bg) 42%, var(--color-surface));
  padding: var(--space-5) var(--space-4);
}

.problem-health__clear-panel--inactive {
  background: color-mix(in srgb, var(--color-surface-sunken) 72%, var(--color-surface));
}

.problem-health__clear-main {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-3);
}

.problem-health__clear-copy {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  line-height: var(--line-normal);
}

.problem-health__clear-copy small {
  color: var(--color-text-faint);
  font-size: var(--text-meta);
  line-height: var(--line-normal);
}

.problem-health__clear-meta {
  flex: 0 0 auto;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  white-space: nowrap;
}

@media (max-width: 860px) {
  .problem-health {
    grid-template-rows: auto auto;
  }

  .problem-health-grid {
    height: auto;
    overflow: visible;
    scrollbar-gutter: auto;
  }

  .problem-health-grid :deep(.ledger-record-list__header) {
    position: static;
  }

  .problem-health-record {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .problem-health-record__identity,
  .problem-health-record__window,
  .problem-health-record__recovery {
    grid-column: 1 / -1;
  }

  .problem-health__clear-panel {
    min-height: 150px;
    height: auto;
  }
}

@media (max-width: 520px) {
  .problem-health-record {
    grid-template-columns: minmax(0, 1fr);
  }

  .problem-health-record__identity,
  .problem-health-record__window,
  .problem-health-record__recovery,
  .problem-health-record__actions {
    grid-column: 1;
  }

  .problem-health-record__actions {
    justify-content: flex-start;
  }

  .problem-health__clear-panel {
    align-items: flex-start;
    flex-direction: column;
    justify-content: center;
    text-align: left;
  }

  .problem-health__clear-main {
    align-items: flex-start;
    flex-direction: column;
  }

  .problem-health__clear-meta {
    white-space: normal;
  }
}
</style>
