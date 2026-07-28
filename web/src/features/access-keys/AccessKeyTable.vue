<script setup lang="ts">
import { Eye, EyeOff, Pencil } from 'lucide-vue-next'
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { revealAccessKey } from '@/api/control/access-keys'
import type { AccessKeyDto, GroupSummary } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import CopyButton from '@/components/ui/CopyButton.vue'
import DataTable from '@/components/ui/DataTable.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'
import { useEphemeralSecret } from './use-ephemeral-secret'

const props = defineProps<{ accessKeys: AccessKeyDto[]; groups: GroupSummary[] }>()
const emit = defineEmits<{ edit: [accessKey: AccessKeyDto, trigger: HTMLElement]; deleted: [] }>()
const client = useApiClient()
const { locale, t } = useI18n()
const secret = useEphemeralSecret()
const revealPending = ref<number | null>(null)
const revealFailed = ref<number | null>(null)
let revealController: AbortController | undefined
let mounted = true

function secretOwner(id: number): string {
  return `access-key:${id}`
}

function revealedSecret(id: number): string | null {
  return secret.read(secretOwner(id))
}

async function toggleReveal(id: number): Promise<void> {
  if (revealedSecret(id)) {
    secret.clear()
    revealFailed.value = null
    return
  }
  revealController?.abort()
  secret.clear()
  const expectedEpoch = secret.epoch.value
  const controller = new AbortController()
  revealController = controller
  revealPending.value = id
  revealFailed.value = null
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
      revealPending.value = null
    }
  }
}

watch(
  () => props.accessKeys.map(({ id }) => id),
  (ids) => {
    const owner = secret.owner.value
    if (owner && !ids.some((id) => secretOwner(id) === owner)) secret.clear()
  },
)

onBeforeUnmount(() => {
  mounted = false
  revealController?.abort()
  revealController = undefined
  secret.clear()
})

function groupSummary(ids: number[]): string {
  if (ids.length === 0) return t('accessKeys.allGroups')
  const names = new Map(props.groups.map((group) => [group.id, group.name]))
  return ids.map((id) => names.get(id) ?? `#${id}`).join(', ')
}

function protocolSummary(protocols: AccessKeyDto['filters']['protocols']): string {
  if (protocols.length === 0) return t('accessKeys.allProtocols')
  return protocols.map((protocol) => t(`common.protocols.${protocol}`)).join(', ')
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
            <code>{{ revealedSecret(accessKey.id) ?? accessKey.masked_key }}</code>
            <button
              type="button"
              class="access-key-table__reveal"
              :aria-label="revealedSecret(accessKey.id) ? t('common.conceal') : t('common.reveal')"
              :aria-pressed="Boolean(revealedSecret(accessKey.id))"
              :data-test="`access-key-reveal-${accessKey.id}`"
              :disabled="revealPending === accessKey.id"
              @click="toggleReveal(accessKey.id)"
            >
              <EyeOff v-if="revealedSecret(accessKey.id)" :size="16" aria-hidden="true" />
              <Eye v-else :size="16" aria-hidden="true" />
            </button>
            <CopyButton
              v-if="revealedSecret(accessKey.id)"
              :value="revealedSecret(accessKey.id) ?? ''"
              :label="t('accessKeys.copy')"
              :success-label="t('common.copied')"
              :failure-label="t('common.copyFailed')"
              :data-test="`access-key-copy-${accessKey.id}`"
            />
            <small v-if="revealFailed === accessKey.id" role="alert">{{
              t('accessKeys.revealFailed')
            }}</small>
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
.access-key-table__reveal {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  cursor: pointer;
}
.access-key-table__secret code {
  overflow-wrap: anywhere;
  color: var(--color-code);
}
.access-key-table__secret small {
  color: var(--color-danger);
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
