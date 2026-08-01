<script setup lang="ts">
import { Pencil } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { revealAccessKey } from '@/app/resources/access-keys'
import type { AccessKeyDto, GroupOptionDto } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppButton from '@/components/ui/AppButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'
import { presentAccessKey } from './access-key-presenter'
import AccessKeySecret from './AccessKeySecret.vue'
import { useEphemeralSecret } from './use-ephemeral-secret'

const props = defineProps<{
  accessKeys: AccessKeyDto[]
  groups: GroupOptionDto[]
  total: number
  page: number
  pageSize: number
}>()
const emit = defineEmits<{
  edit: [accessKey: AccessKeyDto, trigger: HTMLElement]
  deleted: [name: string]
}>()
const client = useApiClient()
const { locale, t } = useI18n()
const secret = useEphemeralSecret()
const revealPending = ref<number>()
const revealFailed = ref<number>()
let revealController: AbortController | undefined
let mounted = true

const presentations = computed(() =>
  props.accessKeys.map((accessKey) =>
    presentAccessKey(accessKey, props.groups, {
      locale: locale.value,
      labels: {
        groups: t('accessKeys.filterGroups'),
        protocols: t('accessKeys.filterProtocols'),
        models: t('accessKeys.filterModels'),
        allGroups: t('accessKeys.allGroups'),
        allProtocols: t('accessKeys.allProtocols'),
        allModels: t('accessKeys.allModels'),
        unlimited: t('accessKeys.unlimited'),
      },
      protocolLabel: (protocol) => t(`common.protocols.${protocol}`),
    }),
  ),
)
const revealedId = computed(() => {
  const owner = secret.owner.value
  if (!owner?.startsWith('access-key:')) return undefined
  const id = Number(owner.slice('access-key:'.length))
  return Number.isSafeInteger(id) ? id : undefined
})
const revealedValue = computed(() =>
  revealedId.value === undefined
    ? undefined
    : (secret.read(secretOwner(revealedId.value)) ?? undefined),
)

function secretOwner(id: number): string {
  return `access-key:${id}`
}

function source(id: number): AccessKeyDto {
  const accessKey = props.accessKeys.find((candidate) => candidate.id === id)
  if (!accessKey) throw new Error(`ACCESS_KEY_SOURCE_MISSING:${id}`)
  return accessKey
}

async function toggleReveal(id: number): Promise<void> {
  if (revealedId.value === id && revealedValue.value) {
    conceal()
    return
  }
  revealController?.abort()
  secret.clear()
  const expectedEpoch = secret.epoch.value
  const controller = new AbortController()
  revealController = controller
  revealPending.value = id
  revealFailed.value = undefined
  try {
    const result = await revealAccessKey(client, id, controller.signal)
    if (mounted && revealController === controller && secret.epoch.value === expectedEpoch) {
      secret.expose(secretOwner(id), result.key)
    }
  } catch (error: unknown) {
    if (mounted && revealController === controller && !(error instanceof RequestCancelledError)) {
      revealFailed.value = id
    }
  } finally {
    if (revealController === controller) {
      revealController = undefined
      revealPending.value = undefined
    }
  }
}

function conceal(): void {
  const controller = revealController
  revealController = undefined
  controller?.abort()
  revealPending.value = undefined
  revealFailed.value = undefined
  secret.clear()
}

defineExpose({ conceal })

watch(
  () => [props.page, props.accessKeys.map(({ id }) => id).join(',')],
  () => conceal(),
)

onBeforeUnmount(() => {
  mounted = false
  conceal()
})
</script>

<template>
  <LedgerRecordList
    :label="t('accessKeys.collection.tableLabel')"
    :row-count="total + 1"
    grid-class="access-keys-record-grid"
  >
    <template #header>
      <span role="columnheader">{{ t('accessKeys.columns.name') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.key') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.status') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.scope') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.rpm') }}</span>
      <span role="columnheader">{{ t('accessKeys.columns.updated') }}</span>
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
        <span :title="record.name">{{ record.name }}</span>
      </div>

      <div class="ledger-record-list__cell access-key-secret-cell" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.key') }}</span>
        <AccessKeySecret
          :id="record.id"
          :masked-key="record.maskedKey"
          :revealed-value="revealedId === record.id ? revealedValue : undefined"
          :pending="revealPending === record.id"
          :failed="revealFailed === record.id"
          @toggle="toggleReveal"
        />
      </div>

      <div class="ledger-record-list__cell access-key-status" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.status') }}</span>
        <StatusBadge :tone="record.status === 'active' ? 'success' : 'neutral'">
          {{ t(`accessKeys.status.${record.status}`) }}
        </StatusBadge>
      </div>

      <div class="ledger-record-list__cell access-key-scope" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.scope') }}</span>
        <dl>
          <div v-for="scope in record.scopeRows" :key="scope.label">
            <dt>{{ scope.label }}</dt>
            <dd :title="scope.value">{{ scope.value }}</dd>
          </div>
        </dl>
      </div>

      <div class="ledger-record-list__cell access-key-rpm" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.rpm') }}</span>
        {{ record.rpm }}
      </div>

      <div class="ledger-record-list__cell access-key-updated" role="cell">
        <span class="mobile-label">{{ t('accessKeys.columns.updated') }}</span>
        <AppDateTime :instant="record.updatedAt" :locale="locale" />
      </div>

      <div class="ledger-record-list__cell record-actions" role="cell">
        <AppButton
          variant="secondary"
          size="compact"
          :aria-label="t('accessKeys.collection.editFor', { name: record.name })"
          @click="emit('edit', source(record.id), $event.currentTarget as HTMLElement)"
        >
          <Pencil :size="15" aria-hidden="true" />
          <span class="edit-label">{{ t('accessKeys.edit') }}</span>
        </AppButton>
        <AccessKeyDeleteDialog
          :access-key="source(record.id)"
          :total="total"
          @deleted="emit('deleted', $event)"
        />
      </div>
    </article>
  </LedgerRecordList>
</template>

<style scoped>
.access-keys-record-grid {
  --ledger-record-list-grid: minmax(132px, 1.08fr) minmax(184px, 1.3fr) 102px minmax(190px, 1.35fr)
    92px minmax(132px, 0.95fr) 150px;
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
.access-key-updated {
  min-width: 0;
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
  font-size: var(--text-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.access-key-rpm,
.access-key-updated {
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
      78px 44px 72px;
    --ledger-record-list-column-gap: 10px;
  }

  .access-key-updated :deep(.app-date-time) {
    display: none;
  }

  .edit-label {
    display: none;
  }
}

@media (max-width: 860px) {
  .access-key-name {
    grid-column: 1 / -1;
    padding-right: 88px;
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
  .access-key-updated {
    display: grid;
    align-content: start;
    gap: 5px;
  }

  .access-key-rpm {
    border-right: 1px solid var(--color-border-subtle);
    padding-right: 12px;
  }

  .record-actions {
    position: absolute;
    top: 10px;
    right: 10px;
  }

  .record-actions :deep(.app-button),
  .record-actions :deep(.access-key-delete__trigger) {
    width: var(--touch-target);
    min-width: var(--touch-target);
    height: var(--touch-target);
    min-height: var(--touch-target);
    padding: 0;
  }

  .edit-label {
    display: none;
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
