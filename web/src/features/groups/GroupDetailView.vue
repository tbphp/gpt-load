<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import {
  groupModelsQueryOptions,
  groupSettingsQueryOptions,
  groupSummaryQueryOptions,
} from '@/app/resources/groups'
import { credentialCollectionQueryOptions } from '@/app/resources/credentials'
import { groupsLocation } from '@/app/route-locations'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'

import GroupHeader from './GroupHeader.vue'
import GroupCredentialsTab from './credentials/GroupCredentialsTab.vue'
import GroupModelsTab from './models/GroupModelsTab.vue'
import GroupTabs from './GroupTabs.vue'
import GroupSettingsTab from './settings/GroupSettingsTab.vue'
import { normalizeGroupTab, parseCredentialRouteQuery, parsePositiveId } from './group-route'

const route = useRoute()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const groupId = computed(() => parsePositiveId(route.params.id))
const activeTab = computed(() => normalizeGroupTab(route.query.tab))
const summaryQuery = useQuery(groupSummaryQueryOptions(client, groupId))
const initialLoading = useStableLoading(
  () => summaryQuery.isPending.value && summaryQuery.data.value === undefined,
)
const summaryRefreshing = computed(
  () => summaryQuery.data.value !== undefined && summaryQuery.isFetching.value,
)

watch(
  groupId,
  (id) => {
    if (id === undefined) return
    void Promise.allSettled([
      queryClient.prefetchQuery(
        credentialCollectionQueryOptions(client, id, parseCredentialRouteQuery(route.query)),
      ),
      queryClient.prefetchQuery(groupModelsQueryOptions(client, id)),
      queryClient.prefetchQuery(groupSettingsQueryOptions(client, id)),
    ])
  },
  { immediate: true },
)
</script>

<template>
  <PageFrame aria-labelledby="group-detail-title">
    <LedgerSheet class="group-detail-page">
      <div v-if="groupId === undefined" class="group-detail-invalid" role="alert">
        <h1 id="group-detail-title">{{ t('group.invalidTitle') }}</h1>
        <p>{{ t('group.invalidDescription') }}</p>
        <RouterLink class="button-link" :to="groupsLocation()">{{
          t('group.backToGroups')
        }}</RouterLink>
      </div>
      <template v-else>
        <AsyncRefreshIndicator :active="summaryRefreshing" :label="t('group.loading')" />
        <SkeletonSurface
          v-if="(summaryQuery.isPending.value && !summaryQuery.data.value) || initialLoading"
          variant="detail"
          :concealed="!initialLoading"
          :label="t('group.loading')"
        />
        <QueryFeedback
          v-else-if="summaryQuery.isError.value && !summaryQuery.data.value"
          state="error"
          :message="t('group.loadFailed')"
          :retry-label="t('common.retry')"
          @retry="summaryQuery.refetch()"
        />
        <template v-else-if="summaryQuery.data.value">
          <QueryFeedback
            v-if="summaryQuery.isError.value"
            state="stale"
            :message="t('group.stale')"
            :retry-label="t('common.retry')"
            @retry="summaryQuery.refetch()"
          />
          <GroupHeader :group="summaryQuery.data.value" />
          <GroupTabs
            :credential-count="summaryQuery.data.value.credential_count"
            :model-count="summaryQuery.data.value.model_count"
          >
            <GroupCredentialsTab
              v-if="activeTab === 'credentials'"
              :key="groupId"
              :group-id="groupId"
            />
            <GroupModelsTab v-else-if="activeTab === 'models'" :key="groupId" :group-id="groupId" />
            <GroupSettingsTab
              v-else-if="activeTab === 'settings'"
              :key="groupId"
              :group-id="groupId"
            />
          </GroupTabs>
        </template>
      </template>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.group-detail-page {
  display: grid;
  min-height: 760px;
  align-content: start;
  gap: 0;
}
@media (max-width: 800px) {
  .group-detail-page {
    min-height: 0;
  }
}
.group-detail-invalid {
  display: grid;
  max-width: 640px;
  gap: var(--space-3);
}
.group-detail-invalid h1,
.group-detail-invalid p {
  margin: 0;
}
.group-detail-invalid p {
  color: var(--color-text-muted);
}
</style>
