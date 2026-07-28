<script setup lang="ts">
import { KeyRound, Plus } from 'lucide-vue-next'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { lazySurface } from '@/app/async-surface'
import { accessKeyListQueryOptions, accessKeyResources } from '@/app/resources/access-keys'
import { groupListQueryOptions } from '@/app/resources/groups'
import type { AccessKeyDto } from '@/api/control/types'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import AppButton from '@/components/ui/AppButton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'

import AccessKeyCollection from './AccessKeyCollection.vue'
import type { PendingAccessKeyCreateOperation } from './access-key-create-operation'
import type { PendingAccessKeyEditOperation } from './access-key-edit-operation'

const AccessKeyDrawer = lazySurface(() => import('./AccessKeyDrawer.vue'))

const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const drawerOpen = ref(false)
const selected = ref<AccessKeyDto | null>(null)
const createOperation = ref<PendingAccessKeyCreateOperation | null>(null)
const editOperation = ref<PendingAccessKeyEditOperation | null>(null)
const viewRoot = ref<HTMLElement | null>(null)
const collection = ref<InstanceType<typeof AccessKeyCollection> | null>(null)
const deletionAnnouncement = ref('')
let restoreFocus: HTMLElement | null = null
let mounted = true

const accessKeysQuery = useQuery(accessKeyListQueryOptions(client))
const groupsQuery = useQuery(groupListQueryOptions(client))
onBeforeUnmount(() => {
  mounted = false
  queryClient.removeQueries({ queryKey: accessKeyResources.list.queryKey, exact: true })
})
const groupCatalogState = computed(() => {
  if (groupsQuery.isError.value) return groupsQuery.data.value ? 'stale' : 'error'
  if (groupsQuery.isPending.value) return 'loading'
  return 'ready'
})
const operationNoticeKey = computed(() =>
  createOperation.value?.state === 'reconciling'
    ? 'accessKeys.operation.reconciling'
    : 'accessKeys.operation.indeterminate',
)
const editOperationNoticeKey = computed(() =>
  editOperation.value?.state === 'reconciling'
    ? 'accessKeys.operation.editReconciling'
    : 'accessKeys.operation.editIndeterminate',
)
const editOperationName = computed(
  () => editOperation.value?.patch.name ?? editOperation.value?.base.name ?? '',
)

function createKey(): void {
  selected.value = null
  restoreFocus = null
  drawerOpen.value = true
}

function editKey(accessKey: AccessKeyDto, trigger: HTMLElement): void {
  if (editOperation.value && editOperation.value.base.id !== accessKey.id) {
    checkEditOperation()
    return
  }
  selected.value = accessKey
  restoreFocus = trigger
  drawerOpen.value = true
}

function setCreateOperation(operation: PendingAccessKeyCreateOperation | null): void {
  createOperation.value = operation
}

function setEditOperation(operation: PendingAccessKeyEditOperation | null): void {
  editOperation.value = operation
}

function checkCreateOperation(): void {
  if (!createOperation.value) return
  selected.value = null
  restoreFocus = null
  drawerOpen.value = true
}

function checkEditOperation(): void {
  const operation = editOperation.value
  if (!operation) return
  selected.value =
    accessKeysQuery.data.value?.find((accessKey) => accessKey.id === operation.base.id) ??
    operation.base
  restoreFocus = null
  drawerOpen.value = true
}

async function setDrawerOpen(open: boolean): Promise<void> {
  drawerOpen.value = open
  if (!open) {
    collection.value?.conceal()
    selected.value = null
    const target = restoreFocus
    restoreFocus = null
    await nextTick()
    target?.focus()
  }
}

async function focusCreateAfterDelete(name: string): Promise<void> {
  deletionAnnouncement.value = ''
  await nextTick()
  await applyInvalidationPlan(queryClient, mutationInvalidationPlans.accessKey.delete)
  await nextTick()
  if (!mounted) return
  deletionAnnouncement.value = t('accessKeys.delete.deletedAnnouncement', { name })
  const target = viewRoot.value?.querySelector('button[data-test="access-key-create"]')
  if (target instanceof HTMLButtonElement && target.isConnected) target.focus()
}
</script>

<template>
  <section ref="viewRoot" class="access-keys" aria-labelledby="access-keys-title">
    <PageHeader
      id="access-keys-title"
      :title="t('accessKeys.title')"
      :description="t('accessKeys.description')"
    >
      <template #actions>
        <AppButton data-test="access-key-create" @click="createKey">
          <Plus :size="16" aria-hidden="true" />{{ t('accessKeys.create') }}
        </AppButton>
      </template>
    </PageHeader>

    <AccessKeyDrawer
      v-if="drawerOpen"
      :open="drawerOpen"
      :access-key="selected"
      :groups="groupsQuery.data.value ?? []"
      :group-catalog-state="groupCatalogState"
      :create-operation="createOperation"
      :edit-operation="selected?.id === editOperation?.base.id ? editOperation : null"
      @update:create-operation="setCreateOperation"
      @update:edit-operation="setEditOperation"
      @update:open="setDrawerOpen"
    />

    <section
      v-if="createOperation"
      class="access-keys__operation"
      data-test="access-key-operation-notice"
      aria-live="polite"
    >
      <InlineFeedback tone="warning">{{ t(operationNoticeKey) }}</InlineFeedback>
      <AppButton
        data-test="access-key-operation-check"
        variant="secondary"
        @click="checkCreateOperation"
      >
        {{ t('accessKeys.operation.checkResult') }}
      </AppButton>
    </section>

    <section
      v-if="editOperation"
      class="access-keys__operation"
      data-test="access-key-edit-operation-notice"
      aria-live="polite"
    >
      <InlineFeedback tone="warning">{{
        t(editOperationNoticeKey, { name: editOperationName })
      }}</InlineFeedback>
      <AppButton
        data-test="access-key-edit-operation-check"
        variant="secondary"
        @click="checkEditOperation"
      >
        {{ t('accessKeys.operation.checkResult') }}
      </AppButton>
    </section>

    <p
      class="sr-only"
      data-test="access-key-delete-announcement"
      aria-live="polite"
      aria-atomic="true"
    >
      {{ deletionAnnouncement }}
    </p>

    <QueryFeedback
      v-if="accessKeysQuery.isPending.value"
      state="loading"
      :message="t('accessKeys.loading')"
    />
    <QueryFeedback
      v-else-if="accessKeysQuery.isError.value && !accessKeysQuery.data.value"
      state="error"
      :message="t('accessKeys.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="accessKeysQuery.refetch()"
    />
    <template v-else>
      <QueryFeedback
        v-if="accessKeysQuery.isError.value"
        state="stale"
        :message="t('accessKeys.stale')"
        :retry-label="t('common.retry')"
        @retry="accessKeysQuery.refetch()"
      />
      <QueryFeedback
        v-if="groupsQuery.isError.value"
        state="stale"
        :message="t('accessKeys.groupsStale')"
        :retry-label="t('common.retry')"
        @retry="groupsQuery.refetch()"
      />
      <EmptyState
        v-if="accessKeysQuery.data.value?.length === 0"
        :title="t('accessKeys.emptyTitle')"
        :description="t('accessKeys.emptyDescription')"
      >
        <template #icon><KeyRound :size="22" aria-hidden="true" /></template>
      </EmptyState>
      <AccessKeyCollection
        v-else
        ref="collection"
        :access-keys="accessKeysQuery.data.value ?? []"
        :groups="groupsQuery.data.value ?? []"
        @edit="editKey"
        @deleted="focusCreateAfterDelete"
      />
    </template>
  </section>
</template>

<style scoped>
.access-keys {
  display: grid;
  gap: var(--space-5);
  min-width: 0;
}
.access-keys__operation {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-card);
  background: var(--color-warning-bg);
  padding: var(--space-3) var(--space-4);
}
@media (max-width: 640px) {
  .access-keys__operation {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
