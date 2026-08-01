<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { lazySurface } from '@/app/async-surface'
import { groupSummaryQueryOptions } from '@/app/resources/groups'
import { groupsLocation } from '@/app/route-locations'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'

import GroupHeader from './GroupHeader.vue'
import GroupTabs from './GroupTabs.vue'
import { normalizeGroupTab, parsePositiveId } from './group-route'

const GroupKeysTab = lazySurface(() => import('./keys/GroupKeysTab.vue'))
const GroupModelsTab = lazySurface(() => import('./models/GroupModelsTab.vue'))
const GroupSettingsTab = lazySurface(() => import('./settings/GroupSettingsTab.vue'))
const route = useRoute()
const client = useApiClient()
const { t } = useI18n()
const groupId = computed(() => parsePositiveId(route.params.id))
const activeTab = computed(() => normalizeGroupTab(route.query.tab))
const summaryQuery = useQuery(groupSummaryQueryOptions(client, groupId))
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
        <QueryFeedback
          v-if="summaryQuery.isPending.value"
          state="loading"
          :message="t('group.loading')"
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
            :key-count="summaryQuery.data.value.key_count"
            :model-count="summaryQuery.data.value.model_count"
          >
            <GroupKeysTab v-if="activeTab === 'keys'" :key="groupId" :group-id="groupId" />
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
  gap: var(--space-5);
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
