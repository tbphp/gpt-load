<script setup lang="ts">
import { ArrowRight, ScrollText } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HealthGroupDto, KeyCounts } from '@/app/resources/health'
import { groupDetailLocation, monitorLocation } from '@/app/route-locations'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import KeyHealthBar from '@/components/ui/KeyHealthBar.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import MonitorSectionHeading from './MonitorSectionHeading.vue'

const props = defineProps<{
  groups: HealthGroupDto[]
  expanded: boolean
}>()
const emit = defineEmits<{ toggle: [] }>()
const { n, t } = useI18n()
const defaultLimit = 5

function status(group: HealthGroupDto) {
  if (!group.enabled) {
    return { key: 'disabled', label: t('monitor.health.groups.disabled'), tone: 'neutral' as const }
  }
  if (group.counts.total === 0) {
    return { key: 'empty', label: t('monitor.health.groups.emptyKeys'), tone: 'danger' as const }
  }
  if (group.counts.disabled === group.counts.total) {
    return {
      key: 'configurationExcluded',
      label: t('monitor.health.groups.configurationExcluded'),
      tone: 'neutral' as const,
    }
  }
  if (group.counts.available === 0) {
    return {
      key: 'unavailable',
      label: t('monitor.health.groups.unavailable'),
      tone: 'danger' as const,
    }
  }
  if (group.counts.cooldown > 0 || group.counts.blacklisted > 0) {
    return {
      key: 'limited',
      label: t('monitor.health.groups.limited'),
      tone: 'warning' as const,
    }
  }
  return { key: 'available', label: t('monitor.health.groups.available'), tone: 'success' as const }
}

function statusRank(group: HealthGroupDto): number {
  const key = status(group).key
  if (key === 'unavailable' || key === 'empty') return 0
  if (key === 'limited') return 1
  if (key === 'available') return 2
  return 3
}

const sortedGroups = computed(() =>
  [...props.groups].sort(
    (left, right) =>
      statusRank(left) - statusRank(right) ||
      left.name.localeCompare(right.name) ||
      left.id - right.id,
  ),
)
const visibleGroups = computed(() =>
  props.expanded ? sortedGroups.value : sortedGroups.value.slice(0, defaultLimit),
)
const canToggle = computed(() => sortedGroups.value.length > defaultLimit)

function keyHealthLabel(counts: KeyCounts): string {
  return t('monitor.health.groups.keyHealthLabel', {
    total: n(counts.total),
    available: n(counts.available),
    cooldown: n(counts.cooldown),
    blacklisted: n(counts.blacklisted),
    disabled: n(counts.disabled),
  })
}
</script>

<template>
  <section class="group-health" aria-labelledby="group-health-title">
    <MonitorSectionHeading
      id="group-health-title"
      :title="t('monitor.health.groups.title')"
      :description="t('monitor.health.groups.description')"
    />

    <div v-if="sortedGroups.length === 0" class="group-health__empty">
      {{ t('monitor.health.groups.empty') }}
    </div>

    <template v-else>
      <LedgerRecordList
        :label="t('monitor.health.groups.tableLabel')"
        :row-count="visibleGroups.length + 1"
        grid-class="group-health-grid"
      >
        <template #header>
          <span role="columnheader">{{ t('monitor.health.groups.columns.group') }}</span>
          <span role="columnheader">{{ t('monitor.health.groups.columns.status') }}</span>
          <span role="columnheader">{{ t('monitor.health.groups.columns.keyHealth') }}</span>
          <span role="columnheader">{{ t('monitor.health.groups.columns.exceptions') }}</span>
          <span role="columnheader">{{ t('monitor.health.groups.columns.actions') }}</span>
        </template>

        <article
          v-for="(group, index) in visibleGroups"
          :key="group.id"
          class="ledger-record-list__record group-health-record"
          role="row"
          :aria-rowindex="index + 2"
        >
          <div class="ledger-record-list__cell group-health-record__identity" role="cell">
            <RouterLink class="group-health-record__name" :to="groupDetailLocation(group.id)">
              {{ group.name }}
            </RouterLink>
            <small>#{{ group.id }}</small>
          </div>

          <div class="ledger-record-list__cell group-health-record__status" role="cell">
            <StatusBadge :tone="status(group).tone" size="compact">
              {{ status(group).label }}
            </StatusBadge>
          </div>

          <div class="ledger-record-list__cell group-health-record__keys" role="cell">
            <KeyHealthBar :counts="group.counts" :label="keyHealthLabel(group.counts)" />
          </div>

          <div class="ledger-record-list__cell group-health-record__exceptions" role="cell">
            <StatusBadge v-if="group.counts.cooldown > 0" tone="warning" size="compact">
              {{ t('monitor.health.groups.cooldownCount', { count: n(group.counts.cooldown) }) }}
            </StatusBadge>
            <StatusBadge v-if="group.counts.blacklisted > 0" tone="danger" size="compact">
              {{
                t('monitor.health.groups.blacklistedCount', {
                  count: n(group.counts.blacklisted),
                })
              }}
            </StatusBadge>
            <span
              v-if="group.counts.cooldown === 0 && group.counts.blacklisted === 0"
              class="group-health-record__none"
            >
              {{ t('monitor.health.groups.none') }}
            </span>
          </div>

          <div class="ledger-record-list__cell group-health-record__actions" role="cell">
            <RouterLink
              v-slot="{ navigate }"
              :to="monitorLocation({ tab: 'logs', group_id: String(group.id) })"
              custom
            >
              <IconButton
                role="link"
                variant="ghost"
                size="compact"
                :label="t('monitor.health.groups.viewLogsFor', { name: group.name })"
                @click="navigate"
              >
                <ScrollText :size="15" aria-hidden="true" />
              </IconButton>
            </RouterLink>
            <RouterLink v-slot="{ navigate }" :to="groupDetailLocation(group.id)" custom>
              <IconButton
                role="link"
                variant="ghost"
                size="compact"
                :label="t('monitor.health.groups.viewGroup', { name: group.name })"
                @click="navigate"
              >
                <ArrowRight :size="15" aria-hidden="true" />
              </IconButton>
            </RouterLink>
          </div>
        </article>
      </LedgerRecordList>

      <footer v-if="canToggle" class="group-health__footer">
        <AppButton variant="link" size="inline" @click="emit('toggle')">
          {{
            expanded
              ? t('monitor.health.groups.collapse')
              : t('monitor.health.groups.showAll', { count: n(sortedGroups.length) })
          }}
        </AppButton>
      </footer>
    </template>
  </section>
</template>

<style scoped>
.group-health {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}

.group-health-grid {
  --ledger-record-list-grid: minmax(170px, 1.25fr) 126px minmax(190px, 1.25fr) minmax(150px, 0.9fr)
    84px;
}

.group-health-record {
  --ledger-record-list-record-min-height: 78px;
  --ledger-record-list-record-padding: 12px 0;
}

.group-health-record__identity,
.group-health-record__exceptions,
.group-health-record__actions {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.group-health-record__identity {
  align-items: flex-start;
  flex-direction: column;
  gap: var(--space-1);
}

.group-health-record__name {
  max-width: 100%;
  color: var(--color-text);
  font-weight: 620;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-health-record__name:hover {
  color: var(--color-action);
}

.group-health-record__identity small,
.group-health-record__none {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.group-health-record__identity small {
  font-family: var(--font-mono);
}

.group-health-record__actions {
  justify-content: flex-end;
}

.group-health__footer {
  display: flex;
  min-height: 42px;
  align-items: center;
  justify-content: flex-end;
  border-bottom: 1px solid var(--color-border-subtle);
}

.group-health__empty {
  border: 1px dashed var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: var(--space-6);
  text-align: center;
}

@media (max-width: 860px) {
  .group-health-record {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .group-health-record__identity,
  .group-health-record__keys {
    grid-column: 1 / -1;
  }

  .group-health-record__actions {
    align-self: center;
  }
}

@media (max-width: 520px) {
  .group-health-record {
    grid-template-columns: minmax(0, 1fr);
  }

  .group-health-record__identity,
  .group-health-record__status,
  .group-health-record__keys,
  .group-health-record__exceptions,
  .group-health-record__actions {
    grid-column: 1;
  }

  .group-health-record__actions {
    justify-content: flex-start;
  }
}
</style>
