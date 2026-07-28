<script setup lang="ts">
import { Pencil } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import type { AccessKeyDto } from '@/api/control/types'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'
import type { AccessKeyPresentation } from './access-key-presenter'
import AccessKeySecret from './AccessKeySecret.vue'

const props = defineProps<{
  accessKeys: AccessKeyDto[]
  presentations: AccessKeyPresentation[]
  revealedId?: number
  revealedValue?: string
  revealPendingId?: number
  revealFailedId?: number
}>()
const emit = defineEmits<{
  toggleReveal: [id: number]
  edit: [accessKey: AccessKeyDto, trigger: HTMLElement]
  deleted: [name: string]
}>()
const { t } = useI18n()

function source(id: number): AccessKeyDto {
  const accessKey = props.accessKeys.find((candidate) => candidate.id === id)
  if (!accessKey) throw new Error(`ACCESS_KEY_SOURCE_MISSING:${id}`)
  return accessKey
}
</script>

<template>
  <DataTable :caption="t('accessKeys.caption')" :scroll-hint="t('accessKeys.scrollHint')" dense>
    <thead>
      <tr>
        <th scope="col" data-column-priority="high">{{ t('accessKeys.columns.name') }}</th>
        <th scope="col" data-column-priority="high">{{ t('accessKeys.columns.key') }}</th>
        <th scope="col" data-column-priority="high">{{ t('accessKeys.columns.status') }}</th>
        <th scope="col" class="access-key-table__scope-column">
          {{ t('accessKeys.columns.filters') }}
        </th>
        <th scope="col">{{ t('accessKeys.columns.rpm') }}</th>
        <th scope="col" data-column-priority="high">{{ t('accessKeys.columns.actions') }}</th>
      </tr>
    </thead>
    <tbody>
      <tr
        v-for="record in presentations"
        :key="record.id"
        :data-test="`access-key-row-${record.id}`"
      >
        <td class="access-key-table__name">{{ record.name }}</td>
        <td>
          <AccessKeySecret
            :id="record.id"
            :masked-key="record.maskedKey"
            :revealed-value="revealedId === record.id ? revealedValue : undefined"
            :pending="revealPendingId === record.id"
            :failed="revealFailedId === record.id"
            @toggle="emit('toggleReveal', $event)"
          />
        </td>
        <td>
          <StatusBadge :tone="record.status === 'active' ? 'success' : 'neutral'">
            {{ t(`accessKeys.status.${record.status}`) }}
          </StatusBadge>
        </td>
        <td class="access-key-table__scope-column">
          <dl class="access-key-table__filters">
            <div v-for="scope in record.scopeRows" :key="scope.label">
              <dt>{{ scope.label }}</dt>
              <dd>{{ scope.value }}</dd>
            </div>
          </dl>
        </td>
        <td class="access-key-table__number">{{ record.rpm }}</td>
        <td>
          <div class="access-key-table__actions">
            <button
              type="button"
              class="access-key-table__edit"
              :data-test="`access-key-edit-${record.id}`"
              @click="emit('edit', source(record.id), $event.currentTarget as HTMLElement)"
            >
              <Pencil :size="16" aria-hidden="true" />{{ t('accessKeys.edit') }}
            </button>
            <AccessKeyDeleteDialog
              :access-key="source(record.id)"
              :total="accessKeys.length"
              @deleted="emit('deleted', $event)"
            />
          </div>
        </td>
      </tr>
    </tbody>
  </DataTable>
</template>

<style scoped>
.access-key-table__name {
  max-width: 18rem;
  font-weight: 700;
  overflow-wrap: anywhere;
}

.access-key-table__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
}

.access-key-table__filters {
  display: grid;
  min-width: 230px;
  gap: var(--space-1);
  margin: 0;
}

.access-key-table__filters div {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  gap: var(--space-2);
}

.access-key-table__filters dt {
  color: var(--color-text-muted);
  font-weight: 650;
}

.access-key-table__filters dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.access-key-table__number {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.access-key-table__edit {
  display: inline-flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-1);
  border: 0;
  background: transparent;
  color: var(--color-action);
  font: inherit;
  font-weight: 650;
  cursor: pointer;
}

@media (min-width: 768px) and (max-width: 1023px) {
  .access-key-table__scope-column {
    display: none;
  }
}
</style>
