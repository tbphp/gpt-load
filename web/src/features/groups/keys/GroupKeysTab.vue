<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import {
  deleteGroupKey,
  upstreamKeyListQueryOptions,
  updateGroupKey,
  type UpstreamKeyDto,
  type UpstreamKeyEffectiveStatus,
} from '@/app/resources/upstream-keys'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { groupDetailLocation } from '@/app/route-locations'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatLocalInstant } from '@/lib/format'

import { buildUpstreamKeyPatch } from './key-patch'

const props = defineProps<{ groupId: number }>()
const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const weights = Array.from({ length: 100 }, (_, index) => index + 1)
const weightDrafts = reactive(new Map<number, string>())
const pendingIds = ref(new Set<number>())
const deleteKeyId = ref<number | null>(null)
const actionError = ref('')
const controllers = new Set<AbortController>()
const keysQuery = useQuery(upstreamKeyListQueryOptions(client, () => props.groupId))
const problemFilterActive = computed(() => route.query.key_state === 'problem')
const visibleKeys = computed(() => {
  const keys = keysQuery.data.value ?? []
  if (!problemFilterActive.value) return keys
  return keys.filter(
    (key) => key.effective_status === 'cooldown' || key.effective_status === 'blacklisted',
  )
})

watch(
  () => keysQuery.data.value,
  (keys) => {
    for (const key of keys ?? []) {
      if (!pendingIds.value.has(key.id)) {
        weightDrafts.set(key.id, key.weight_manual === null ? 'auto' : String(key.weight_manual))
      }
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  for (const controller of controllers) controller.abort()
  controllers.clear()
})

function pending(keyId: number): boolean {
  return pendingIds.value.has(keyId)
}

function setPending(keyId: number, value: boolean): void {
  const next = new Set(pendingIds.value)
  if (value) next.add(keyId)
  else next.delete(keyId)
  pendingIds.value = next
}

function selectedWeight(key: UpstreamKeyDto): number | null {
  const value =
    weightDrafts.get(key.id) ?? (key.weight_manual === null ? 'auto' : String(key.weight_manual))
  return value === 'auto' ? null : Number(value)
}

function hasWeightPatch(key: UpstreamKeyDto): boolean {
  return (
    Object.keys(
      buildUpstreamKeyPatch(
        { status: key.status, weight_manual: key.weight_manual },
        { status: key.status, weight_manual: selectedWeight(key) },
      ),
    ).length > 0
  )
}

function statusTone(
  status: UpstreamKeyEffectiveStatus,
): 'success' | 'warning' | 'danger' | 'neutral' {
  if (status === 'available') return 'success'
  if (status === 'cooldown') return 'warning'
  if (status === 'blacklisted') return 'danger'
  return 'neutral'
}

function formatCooldown(value: number | null): string {
  return value === null ? t('group.keys.none') : formatLocalInstant(value, locale.value)
}

function clearProblemFilter(): void {
  void router.push(groupDetailLocation(String(route.params.id), { tab: 'keys' }))
}

async function runUpdate(
  key: UpstreamKeyDto,
  patch: ReturnType<typeof buildUpstreamKeyPatch>,
): Promise<void> {
  if (Object.keys(patch).length === 0 || pending(key.id)) return
  actionError.value = ''
  setPending(key.id, true)
  const controller = new AbortController()
  controllers.add(controller)
  try {
    await updateGroupKey(client, props.groupId, key.id, patch, controller.signal)
    await applyInvalidationPlan(
      queryClient,
      mutationInvalidationPlans.upstreamKey.update(props.groupId),
    )
  } catch {
    actionError.value = t('group.keys.updateFailed')
  } finally {
    controllers.delete(controller)
    setPending(key.id, false)
  }
}

function saveWeight(key: UpstreamKeyDto): void {
  const patch = buildUpstreamKeyPatch(
    { status: key.status, weight_manual: key.weight_manual },
    { status: key.status, weight_manual: selectedWeight(key) },
  )
  void runUpdate(key, patch)
}

function toggleStatus(key: UpstreamKeyDto): void {
  const patch = buildUpstreamKeyPatch(
    { status: key.status, weight_manual: key.weight_manual },
    {
      status: key.status === 'active' ? 'disabled' : 'active',
      weight_manual: key.weight_manual,
    },
  )
  void runUpdate(key, patch)
}

function setDeleteDialog(open: boolean, keyId: number): void {
  if (pending(keyId)) return
  deleteKeyId.value = open ? keyId : null
}

async function confirmDelete(key: UpstreamKeyDto): Promise<void> {
  if (pending(key.id)) return
  actionError.value = ''
  setPending(key.id, true)
  const controller = new AbortController()
  controllers.add(controller)
  try {
    await deleteGroupKey(client, props.groupId, key.id, controller.signal)
    await applyInvalidationPlan(
      queryClient,
      mutationInvalidationPlans.upstreamKey.delete(props.groupId),
    )
    deleteKeyId.value = null
  } catch {
    actionError.value = t('group.keys.deleteFailed')
  } finally {
    controllers.delete(controller)
    setPending(key.id, false)
  }
}

const statusLabels = computed(() => ({
  active: t('group.keys.status.active'),
  disabled: t('group.keys.status.disabled'),
}))
const effectiveLabels = computed(() => ({
  available: t('group.keys.effective.available'),
  cooldown: t('group.keys.effective.cooldown'),
  blacklisted: t('group.keys.effective.blacklisted'),
  disabled: t('group.keys.effective.disabled'),
}))
</script>

<template>
  <section class="group-keys" aria-labelledby="group-keys-heading">
    <header class="group-keys__header">
      <div>
        <h2 id="group-keys-heading">{{ t('group.keys.title') }}</h2>
        <p>{{ t('group.keys.description') }}</p>
      </div>
    </header>

    <QueryFeedback
      v-if="keysQuery.isPending.value"
      state="loading"
      :message="t('group.keys.loading')"
    />
    <QueryFeedback
      v-else-if="keysQuery.isError.value && !keysQuery.data.value"
      state="error"
      :message="t('group.keys.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="keysQuery.refetch()"
    />
    <template v-else>
      <QueryFeedback
        v-if="keysQuery.isError.value"
        state="stale"
        :message="t('group.keys.stale')"
        :retry-label="t('common.retry')"
        @retry="keysQuery.refetch()"
      />
      <p v-if="actionError" class="group-keys__action-error" role="alert">{{ actionError }}</p>
      <div v-if="problemFilterActive" class="group-keys__filter" role="status">
        <p>{{ t('group.keys.problemFilter') }}</p>
        <AppButton variant="ghost" size="sm" @click="clearProblemFilter">
          {{ t('group.keys.clearProblemFilter') }}
        </AppButton>
      </div>
      <EmptyState
        v-if="keysQuery.data.value?.length === 0"
        :title="t('group.keys.emptyTitle')"
        :description="t('group.keys.emptyDescription')"
      />
      <EmptyState
        v-else-if="visibleKeys.length === 0"
        :title="t('group.keys.problemEmptyTitle')"
        :description="t('group.keys.problemEmptyDescription')"
      />
      <DataTable v-else :caption="t('group.keys.caption')">
        <thead>
          <tr>
            <th scope="col">{{ t('group.keys.columns.key') }}</th>
            <th scope="col">{{ t('group.keys.columns.configured') }}</th>
            <th scope="col">{{ t('group.keys.columns.effective') }}</th>
            <th scope="col">{{ t('group.keys.columns.manualWeight') }}</th>
            <th scope="col">{{ t('group.keys.columns.autoWeight') }}</th>
            <th scope="col">{{ t('group.keys.columns.cooldown') }}</th>
            <th scope="col">{{ t('group.keys.columns.failures') }}</th>
            <th scope="col">{{ t('group.keys.columns.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="key in visibleKeys" :key="key.id">
            <td class="group-keys__mask">{{ key.mask }}</td>
            <td>
              <StatusBadge :tone="key.status === 'active' ? 'success' : 'neutral'">
                {{ statusLabels[key.status] }}
              </StatusBadge>
            </td>
            <td>
              <StatusBadge :tone="statusTone(key.effective_status)">
                {{ effectiveLabels[key.effective_status] }}
              </StatusBadge>
            </td>
            <td>
              <select
                :aria-label="t('group.keys.weightFor', { mask: key.mask })"
                :value="weightDrafts.get(key.id)"
                :disabled="pending(key.id)"
                @change="weightDrafts.set(key.id, ($event.target as HTMLSelectElement).value)"
              >
                <option value="auto">{{ t('group.keys.auto') }}</option>
                <option v-if="key.weight_manual === 0" value="0" disabled>0</option>
                <option v-for="weight in weights" :key="weight" :value="String(weight)">
                  {{ weight }}
                </option>
              </select>
            </td>
            <td class="group-keys__number">{{ key.weight_auto }}</td>
            <td class="group-keys__number">{{ formatCooldown(key.cooldown_until_ms) }}</td>
            <td class="group-keys__number">{{ key.failure_count }}</td>
            <td>
              <div class="group-keys__actions">
                <AppButton variant="ghost" :busy="pending(key.id)" @click="toggleStatus(key)">
                  {{ key.status === 'active' ? t('group.keys.disable') : t('group.keys.enable') }}
                </AppButton>
                <AppButton
                  variant="secondary"
                  :disabled="!hasWeightPatch(key)"
                  :busy="pending(key.id)"
                  @click="saveWeight(key)"
                >
                  {{ t('group.keys.saveWeight') }}
                </AppButton>
                <AppConfirmDialog
                  :open="deleteKeyId === key.id"
                  :title="t('group.keys.deleteTitle')"
                  :description="t('group.keys.deleteDescription', { mask: key.mask })"
                  :close-label="t('group.keys.closeDialog')"
                  :cancel-label="t('group.keys.cancel')"
                  :confirm-label="t('group.keys.confirmDelete')"
                  tone="danger"
                  :pending="pending(key.id)"
                  @update:open="setDeleteDialog($event, key.id)"
                  @confirm="confirmDelete(key)"
                >
                  <template #trigger>
                    <AppButton
                      class="group-keys__delete"
                      variant="ghost"
                      :aria-disabled="pending(key.id) ? 'true' : undefined"
                      @click="!pending(key.id) && (deleteKeyId = key.id)"
                    >
                      {{ t('group.keys.delete') }}
                    </AppButton>
                  </template>
                </AppConfirmDialog>
              </div>
            </td>
          </tr>
        </tbody>
      </DataTable>
    </template>
  </section>
</template>

<style scoped>
.group-keys {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}
.group-keys__header h2 {
  margin: 0;
  font-size: 1.125rem;
}
.group-keys__header p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}
.group-keys__filter {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  background: var(--color-warning-bg);
  padding: var(--space-2) var(--space-3);
}
.group-keys__filter p {
  margin: 0;
  color: var(--color-text-muted);
}
.group-keys__mask,
.group-keys__number {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.group-keys select {
  min-width: 92px;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: 7px 9px;
  font: inherit;
  cursor: pointer;
}
.group-keys__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.group-keys__actions :deep(.app-button) {
  padding-inline: var(--space-2);
  font-size: 0.8125rem;
}
.group-keys__delete :deep(button),
.group-keys__delete {
  color: var(--color-danger);
}
.group-keys__action-error {
  margin: 0;
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  color: var(--color-danger);
  padding: var(--space-3);
}
@media (max-width: 640px) {
  .group-keys select {
    font-size: 16px;
  }
}
</style>
