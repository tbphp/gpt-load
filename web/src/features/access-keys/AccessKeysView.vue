<script setup lang="ts">
import { KeyRound, Plus } from 'lucide-vue-next'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { listAccessKeys } from '@/api/control/access-keys'
import { listGroups } from '@/api/control/groups'
import type { AccessKeyDto } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import { accessKeyMutationInvalidations, accessKeyResources } from '@/app/resources/access-keys'
import AppButton from '@/components/ui/AppButton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'

import AccessKeyDrawer from './AccessKeyDrawer.vue'
import AccessKeyTable from './AccessKeyTable.vue'
import type { PendingAccessKeyCreateOperation } from './access-key-create-operation'

const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const drawerOpen = ref(false)
const selected = ref<AccessKeyDto | null>(null)
const createOperation = ref<PendingAccessKeyCreateOperation | null>(null)
const viewRoot = ref<HTMLElement | null>(null)
const deletionAnnouncement = ref('')
let restoreFocus: HTMLElement | null = null
let mounted = true

const accessKeysQuery = useQuery({
  queryKey: accessKeyResources.list.queryKey,
  queryFn: ({ signal }) => listAccessKeys(client, signal),
  gcTime: accessKeyResources.list.gcTime,
})
const groupsQuery = useQuery({
  queryKey: controlQueryKeys.groups.list(),
  queryFn: ({ signal }) => listGroups(client, signal),
})
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

function createKey(): void {
  selected.value = null
  restoreFocus = null
  drawerOpen.value = true
}

function editKey(accessKey: AccessKeyDto, trigger: HTMLElement): void {
  selected.value = accessKey
  restoreFocus = trigger
  drawerOpen.value = true
}

function setCreateOperation(operation: PendingAccessKeyCreateOperation | null): void {
  createOperation.value = operation
}

function checkCreateOperation(): void {
  if (!createOperation.value) return
  selected.value = null
  restoreFocus = null
  drawerOpen.value = true
}

async function setDrawerOpen(open: boolean): Promise<void> {
  drawerOpen.value = open
  if (!open) {
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
  await Promise.all(
    accessKeyMutationInvalidations.delete.map((queryKey) =>
      queryClient.invalidateQueries({ queryKey, exact: true }),
    ),
  )
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
        <AccessKeyDrawer
          :open="drawerOpen"
          :access-key="selected"
          :groups="groupsQuery.data.value ?? []"
          :group-catalog-state="groupCatalogState"
          :create-operation="createOperation"
          @update:create-operation="setCreateOperation"
          @update:open="setDrawerOpen"
        >
          <template #trigger>
            <AppButton data-test="access-key-create" @click="createKey">
              <Plus :size="16" aria-hidden="true" />{{ t('accessKeys.create') }}
            </AppButton>
          </template>
        </AccessKeyDrawer>
      </template>
    </PageHeader>

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
      <AccessKeyTable
        v-else
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
