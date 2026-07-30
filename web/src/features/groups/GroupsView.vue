<script setup lang="ts">
import { ArrowRight, Layers3 } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { groupListQueryOptions } from '@/app/resources/groups'
import { groupDetailLocation } from '@/app/route-locations'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const client = useApiClient()
const { n, t } = useI18n()
const groupsQuery = useQuery(groupListQueryOptions(client))
</script>

<template>
  <section class="groups" aria-labelledby="groups-title">
    <PageHeader
      id="groups-title"
      :title="t('groups.title')"
      :description="t('groups.description')"
    />

    <QueryFeedback
      v-if="groupsQuery.isPending.value"
      state="loading"
      :message="t('groups.loading')"
    />
    <QueryFeedback
      v-else-if="groupsQuery.isError.value && !groupsQuery.data.value"
      state="error"
      :message="t('groups.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="groupsQuery.refetch()"
    />
    <template v-else>
      <QueryFeedback
        v-if="groupsQuery.isError.value"
        state="stale"
        :message="t('groups.stale')"
        :retry-label="t('common.retry')"
        @retry="groupsQuery.refetch()"
      />
      <EmptyState
        v-if="groupsQuery.data.value?.length === 0"
        :title="t('groups.emptyTitle')"
        :description="t('groups.emptyDescription')"
      >
        <template #icon><Layers3 :size="22" aria-hidden="true" /></template>
      </EmptyState>
      <DataTable v-else :caption="t('groups.caption')" :scroll-hint="t('groups.scrollHint')">
        <thead>
          <tr>
            <th scope="col" data-column-priority="high">{{ t('groups.columns.name') }}</th>
            <th scope="col" data-column-priority="high">{{ t('groups.columns.status') }}</th>
            <th scope="col">{{ t('groups.columns.keys') }}</th>
            <th scope="col">{{ t('groups.columns.models') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="group in groupsQuery.data.value ?? []" :key="group.id">
            <td>
              <RouterLink
                class="groups__detail"
                :to="groupDetailLocation(group.id)"
                :aria-label="t('groups.open', { name: group.name })"
              >
                <span>{{ group.name }}</span>
                <ArrowRight :size="15" aria-hidden="true" />
              </RouterLink>
            </td>
            <td>
              <StatusBadge :tone="group.enabled ? 'success' : 'neutral'">
                {{ t(group.enabled ? 'groups.enabled' : 'groups.disabled') }}
              </StatusBadge>
            </td>
            <td>{{ n(group.key_count) }}</td>
            <td>{{ n(group.models.length) }}</td>
          </tr>
        </tbody>
      </DataTable>
    </template>
  </section>
</template>

<style scoped>
.groups {
  display: grid;
  min-width: 0;
  gap: var(--space-5);
}

.groups__detail {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text);
  font-weight: 650;
}

.groups__detail:hover {
  color: var(--color-action);
}
</style>
