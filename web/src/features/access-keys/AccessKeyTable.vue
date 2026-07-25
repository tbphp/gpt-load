<script setup lang="ts">
import { Pencil } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import type { AccessKeyDto, GroupSummary } from '@/api/control/types'
import CopyButton from '@/components/ui/CopyButton.vue'
import DataTable from '@/components/ui/DataTable.vue'
import SecretValue from '@/components/ui/SecretValue.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'

const props = defineProps<{ accessKeys: AccessKeyDto[]; groups: GroupSummary[] }>()
const emit = defineEmits<{ edit: [accessKey: AccessKeyDto, trigger: HTMLElement]; deleted: [] }>()
const { locale, t } = useI18n()

function groupSummary(ids: number[]): string {
  if (ids.length === 0) return t('accessKeys.allGroups')
  const names = new Map(props.groups.map((group) => [group.id, group.name]))
  return ids.map((id) => names.get(id) ?? `#${id}`).join(', ')
}

function protocolSummary(protocols: AccessKeyDto['filters']['protocols']): string {
  if (protocols.length === 0) return t('accessKeys.allProtocols')
  return protocols.map((protocol) => t(`group.protocols.${protocol}`)).join(', ')
}

function modelSummary(models: string[]): string {
  return models.length === 0 ? t('accessKeys.allModels') : models.join(', ')
}

function rpmSummary(rpm: number): string {
  return rpm === 0 ? t('accessKeys.unlimited') : new Intl.NumberFormat(locale.value).format(rpm)
}
</script>

<template>
  <DataTable :caption="t('accessKeys.caption')" dense>
    <thead>
      <tr>
        <th scope="col">{{ t('accessKeys.columns.name') }}</th>
        <th scope="col">{{ t('accessKeys.columns.key') }}</th>
        <th scope="col">{{ t('accessKeys.columns.status') }}</th>
        <th scope="col">{{ t('accessKeys.columns.filters') }}</th>
        <th scope="col">{{ t('accessKeys.columns.rpm') }}</th>
        <th scope="col">{{ t('accessKeys.columns.actions') }}</th>
      </tr>
    </thead>
    <tbody>
      <tr
        v-for="accessKey in accessKeys"
        :key="accessKey.id"
        :data-test="`access-key-row-${accessKey.id}`"
      >
        <td class="access-key-table__name">{{ accessKey.name }}</td>
        <td>
          <div class="access-key-table__secret">
            <SecretValue
              :value="accessKey.key"
              :reveal-label="t('common.reveal')"
              :conceal-label="t('common.conceal')"
              :button-test="`access-key-reveal-${accessKey.id}`"
            />
            <CopyButton
              :value="accessKey.key"
              :label="t('accessKeys.copy')"
              :success-label="t('common.copied')"
              :failure-label="t('common.copyFailed')"
              :data-test="`access-key-copy-${accessKey.id}`"
            />
          </div>
        </td>
        <td>
          <StatusBadge :tone="accessKey.status === 'active' ? 'success' : 'neutral'">
            {{ t(`accessKeys.status.${accessKey.status}`) }}
          </StatusBadge>
        </td>
        <td>
          <dl class="access-key-table__filters">
            <div>
              <dt>{{ t('accessKeys.filterGroups') }}</dt>
              <dd>{{ groupSummary(accessKey.filters.groups) }}</dd>
            </div>
            <div>
              <dt>{{ t('accessKeys.filterProtocols') }}</dt>
              <dd>{{ protocolSummary(accessKey.filters.protocols) }}</dd>
            </div>
            <div>
              <dt>{{ t('accessKeys.filterModels') }}</dt>
              <dd>{{ modelSummary(accessKey.filters.models) }}</dd>
            </div>
          </dl>
        </td>
        <td class="access-key-table__number">{{ rpmSummary(accessKey.rpm_limit) }}</td>
        <td>
          <div class="access-key-table__actions">
            <button
              type="button"
              class="access-key-table__edit"
              :data-test="`access-key-edit-${accessKey.id}`"
              @click="emit('edit', accessKey, $event.currentTarget as HTMLElement)"
            >
              <Pencil :size="16" aria-hidden="true" />{{ t('accessKeys.edit') }}
            </button>
            <AccessKeyDeleteDialog
              :access-key="accessKey"
              :total="accessKeys.length"
              @deleted="emit('deleted')"
            />
          </div>
        </td>
      </tr>
    </tbody>
  </DataTable>
</template>

<style scoped>
.access-key-table__name {
  font-weight: 700;
}
.access-key-table__secret,
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
  min-height: 44px;
  align-items: center;
  gap: var(--space-1);
  border: 0;
  background: transparent;
  color: var(--color-primary);
  font: inherit;
  font-weight: 650;
  cursor: pointer;
}
</style>
