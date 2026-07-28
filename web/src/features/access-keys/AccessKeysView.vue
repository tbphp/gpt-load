<script setup lang="ts">
import { KeyRound, Plus } from 'lucide-vue-next'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { listAccessKeys } from '@/api/control/access-keys'
import { listGroups } from '@/api/control/groups'
import type { AccessKeyDto } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import AppButton from '@/components/ui/AppButton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'

import AccessKeyDrawer from './AccessKeyDrawer.vue'
import AccessKeyTable from './AccessKeyTable.vue'

const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const drawerOpen = ref(false)
const selected = ref<AccessKeyDto | null>(null)
const viewRoot = ref<HTMLElement | null>(null)
let restoreFocus: HTMLElement | null = null
let mounted = true

const accessKeysQuery = useQuery({
  queryKey: controlQueryKeys.accessKeys.list(),
  queryFn: ({ signal }) => listAccessKeys(client, signal),
  gcTime: 0,
})
const groupsQuery = useQuery({
  queryKey: controlQueryKeys.groups.list(),
  queryFn: ({ signal }) => listGroups(client, signal),
})
onBeforeUnmount(() => {
  mounted = false
  queryClient.removeQueries({ queryKey: controlQueryKeys.accessKeys.list(), exact: true })
})

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

async function focusCreateAfterDelete(): Promise<void> {
  await queryClient.invalidateQueries({
    queryKey: controlQueryKeys.accessKeys.list(),
    exact: true,
  })
  await nextTick()
  if (!mounted) return
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
</style>
