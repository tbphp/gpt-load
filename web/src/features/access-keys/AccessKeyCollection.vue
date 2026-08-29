<script setup lang="ts">
import { ArrowRight, RotateCcw, Trash2 } from '@lucide/vue'
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import type { AccessKeyCollectionItemDto, GroupOptionDto } from '@/api/control/types'
import { revealAccessKey } from '@/app/resources/access-keys'
import { useAbortControllerPool } from '@/app/use-abort-controller-pool'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'
import AccessKeyCostLimitResetDialog from './AccessKeyCostLimitResetDialog.vue'
import { presentAccessKeyCollection } from './access-key-presenter'

const props = defineProps<{
  accessKeys: readonly AccessKeyCollectionItemDto[]
  groups: readonly GroupOptionDto[]
  total: number
  filteredTotal: number
  page: number
  pageSize: number
  busyIds: ReadonlySet<number>
  lockedIds: ReadonlySet<number>
}>()
const emit = defineEmits<{
  open: [accessKey: AccessKeyCollectionItemDto, trigger: HTMLElement]
  toggle: [accessKey: AccessKeyCollectionItemDto]
  deleted: [name: string]
  reset: [name: string]
}>()
const client = useApiClient()
const { locale, t } = useI18n()
const copyControllers = useAbortControllerPool()
const sources = computed(
  () => new Map(props.accessKeys.map((accessKey) => [accessKey.id, accessKey])),
)

const presentations = computed(() =>
  presentAccessKeyCollection(props.accessKeys, props.groups, {
    locale: locale.value,
    labels: {
      groups: t('accessKeys.filterGroups'),
      protocols: t('accessKeys.filterProtocols'),
      models: t('accessKeys.filterModels'),
      allGroups: t('accessKeys.allGroups'),
      allProtocols: t('accessKeys.allProtocols'),
      allModels: t('accessKeys.allModels'),
      unlimited: t('accessKeys.unlimited'),
      costRules: (count) => t('accessKeys.costLimits.ruleCount', { count }),
    },
    protocolLabel: (protocol) =>
      protocol === 'openai-embeddings' ? t('common.protocols.openaiEmbeddings.label') : protocol,
  }),
)
function source(id: number): AccessKeyCollectionItemDto {
  const accessKey = sources.value.get(id)
  if (!accessKey) throw new Error(`ACCESS_KEY_SOURCE_MISSING:${id}`)
  return accessKey
}

async function resolveCopyValue(id: number): Promise<string> {
  const controller = copyControllers.create()
  try {
    const result = await revealAccessKey(client, id, controller.signal)
    return result.key
  } finally {
    copyControllers.release(controller)
  }
}

function conceal(): void {
  copyControllers.abortAll()
}

defineExpose({ conceal })

watch(
  () => [props.page, props.accessKeys.map(({ id }) => id).join(',')],
  () => conceal(),
)
</script>

<template>
  <LedgerRecordList
    :label="t('accessKeys.collection.tableLabel')"
    :row-count="filteredTotal + 1"
    grid-class="access-keys-record-grid"
  >
    <template #header>
      <span role="columnheader">{{ t('accessKeys.columns.name') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.key') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.status') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.scope') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.limits') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.lastRequest') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.actions') }}</span>
    </template>

    <article
      v-for="(record, index) in presentations"
      :key="record.id"
      class="ledger-record-list__record access-key-record"
      role="row"
      :aria-rowindex="(page - 1) * pageSize + index + 2"
    >
      <div class="ledger-record-list__cell access-key-name" role="cell">
        <span>{{ record.name }}</span>
      </div>

      <div class="ledger-record-list__cell access-key-secret-cell" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.key') }}</span>
        <CopyChip
          :value="record.maskedKey"
          :label="t('accessKeys.copy')"
          :success-label="t('common.copied')"
          :failure-label="t('common.copyFailed')"
          :resolve-value="() => resolveCopyValue(record.id)"
        />
      </div>

      <div class="ledger-record-list__cell access-key-status" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.status') }}</span>
        <StatusBadge :tone="record.status === 'active' ? 'success' : 'neutral'">
          {{ t(`accessKeys.status.${record.status}`) }}
        </StatusBadge>
        <StatusBadge v-if="record.expired" tone="danger" size="compact">
          {{ t('accessKeys.status.expired') }}
        </StatusBadge>
        <StatusBadge v-if="record.ipRestricted" tone="neutral" size="compact">
          {{ t('accessKeys.status.ipRestricted') }}
        </StatusBadge>
        <StatusBadge v-if="record.quotaExhausted" tone="danger" size="compact">
          {{ t('accessKeys.costLimits.exhausted') }}
        </StatusBadge>
      </div>

      <div class="ledger-record-list__cell access-key-scope" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.scope') }}</span>
        <dl>
          <div v-for="scope in record.scopeRows" :key="scope.label">
            <dt>{{ scope.label }}</dt>
            <OverflowTooltip as="dd" :content="scope.value">{{ scope.value }}</OverflowTooltip>
          </div>
        </dl>
      </div>

      <div class="ledger-record-list__cell access-key-rpm" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.limits') }}</span>
        <span v-for="limit in record.limits" :key="limit">{{ limit }}</span>
      </div>

      <div class="ledger-record-list__cell access-key-last-request" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.lastRequest') }}</span>
        <AppRelativeTime
          :instant="record.lastRequestAt"
          :locale="locale"
          :empty-label="t('accessKeys.collection.neverRequested')"
        />
      </div>

      <div class="ledger-record-list__cell record-actions" role="cell">
        <AppButton
          variant="secondary"
          :tone="record.status === 'active' ? 'warning' : 'success'"
          size="compact"
          :busy="busyIds.has(record.id)"
          :disabled="lockedIds.has(record.id)"
          @click="emit('toggle', source(record.id))"
        >
          {{
            record.status === 'active'
              ? t('accessKeys.actions.disable')
              : t('accessKeys.actions.enable')
          }}
        </AppButton>
        <AccessKeyCostLimitResetDialog
          v-if="record.costLimitRuleCount > 0"
          :access-key="source(record.id)"
          @reset="emit('reset', $event)"
        >
          <template #trigger="{ open }">
            <IconButton
              variant="ghost"
              size="compact"
              :label="t('accessKeys.reset.open')"
              :disabled="busyIds.has(record.id) || lockedIds.has(record.id)"
              @click="open"
            >
              <RotateCcw :size="15" aria-hidden="true" />
            </IconButton>
          </template>
        </AccessKeyCostLimitResetDialog>
        <AccessKeyDeleteDialog
          :access-key="source(record.id)"
          :total="total"
          @deleted="emit('deleted', $event)"
        >
          <template #trigger="{ open }">
            <IconButton
              variant="ghost"
              tone="danger"
              size="compact"
              :label="t('accessKeys.delete.open')"
              :disabled="busyIds.has(record.id) || lockedIds.has(record.id)"
              @click="open"
            >
              <Trash2 :size="15" aria-hidden="true" />
            </IconButton>
          </template>
        </AccessKeyDeleteDialog>
        <IconButton
          variant="ghost"
          size="compact"
          :label="t('accessKeys.collection.openDetailsFor', { name: record.name })"
          :disabled="busyIds.has(record.id) || lockedIds.has(record.id)"
          @click="emit('open', source(record.id), $event.currentTarget as HTMLElement)"
        >
          <ArrowRight :size="15" aria-hidden="true" />
        </IconButton>
      </div>
    </article>
  </LedgerRecordList>
</template>

<style scoped>
.access-keys-record-grid {
  --ledger-record-list-grid: minmax(132px, 1.08fr) minmax(184px, 1.3fr) 102px minmax(190px, 1.35fr)
    92px minmax(126px, 0.9fr) minmax(158px, 1.05fr);
  --ledger-record-list-column-gap: 14px;
}

.access-key-name {
  min-width: 0;
  color: var(--color-text);
  font-weight: 600;
  overflow-wrap: anywhere;
}

.access-key-secret-cell,
.access-key-status,
.access-key-scope,
.access-key-rpm,
.access-key-last-request {
  min-width: 0;
}

.access-key-rpm {
  display: grid;
  gap: 2px;
}

.access-key-status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
}

.access-key-scope dl {
  display: grid;
  gap: 3px;
  margin: 0;
}

.access-key-scope dl > div {
  display: grid;
  min-width: 0;
  grid-template-columns: 46px minmax(0, 1fr);
  align-items: baseline;
  gap: 6px;
}

.access-key-scope dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-align: right;
}

.access-key-scope dd {
  min-width: 0;
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.access-key-rpm,
.access-key-last-request {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

.record-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

.mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

@media (max-width: 1120px) {
  .access-keys-record-grid {
    --ledger-record-list-grid: minmax(110px, 1.05fr) minmax(156px, 1.2fr) 90px minmax(164px, 1.25fr)
      78px minmax(96px, 0.8fr) minmax(148px, 1fr);
    --ledger-record-list-column-gap: 10px;
  }

  .access-key-last-request :deep(.app-relative-time) {
    line-height: var(--line-compact);
    overflow-wrap: anywhere;
    white-space: normal;
  }
}

@media (max-width: 1023px) and (min-width: 861px) {
  .access-keys-record-grid {
    --ledger-record-list-grid: minmax(110px, 1fr) minmax(156px, 1.2fr) 90px minmax(164px, 1.25fr)
      minmax(144px, 1fr);
  }

  .access-keys-record-grid :deep(.ledger-record-list__header > :nth-child(5)),
  .access-keys-record-grid :deep(.ledger-record-list__header > :nth-child(6)),
  .access-key-rpm,
  .access-key-last-request {
    display: none;
  }
}

@media (max-width: 860px) {
  .access-key-name {
    grid-column: 1 / -1;
  }

  .access-key-secret-cell {
    grid-column: 1 / -1;
    border-top: 1px solid var(--color-border-subtle);
    border-bottom: 1px solid var(--color-border-subtle);
    padding: 11px 0;
  }

  .access-key-scope {
    grid-column: 1 / -1;
  }

  .access-key-status,
  .access-key-rpm,
  .access-key-last-request {
    display: grid;
    align-content: start;
    gap: 5px;
  }

  .access-key-rpm {
    border-right: 1px solid var(--color-border-subtle);
    padding-right: 12px;
  }

  .record-actions {
    grid-column: 1 / -1;
    flex-wrap: wrap;
    border-top: 1px solid var(--color-border-subtle);
    padding-top: 11px;
  }

  .record-actions :deep(.app-button),
  .record-actions :deep(.icon-button) {
    min-height: var(--touch-target);
  }

  .mobile-label {
    display: inline;
  }
}

@media (max-width: 560px) {
  .access-keys-record-grid {
    --ledger-record-list-card-grid: 76px minmax(0, 1fr);
  }

  .access-key-scope dl > div {
    grid-template-columns: 42px minmax(0, 1fr);
  }
}
</style>
