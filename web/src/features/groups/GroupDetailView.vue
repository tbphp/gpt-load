<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { getGroup } from '@/api/control/groups'
import { controlQueryKeys } from '@/app/query-keys'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import GroupHeader from './GroupHeader.vue'
import GroupTabs from './GroupTabs.vue'
import { normalizeGroupTab, parsePositiveId } from './group-route'
import GroupKeysTab from './keys/GroupKeysTab.vue'
import GroupModelsTab from './models/GroupModelsTab.vue'

const route = useRoute()
const client = useApiClient()
const { t } = useI18n()
const groupId = computed(() => parsePositiveId(route.params.id))
const activeTab = computed(() => normalizeGroupTab(route.query.tab))
const detailQuery = useQuery({
  queryKey: computed(() =>
    groupId.value
      ? controlQueryKeys.groups.detail(groupId.value)
      : controlQueryKeys.groups.details(),
  ),
  queryFn: ({ signal }) => {
    const id = groupId.value
    if (!id) throw new Error('INVALID_GROUP_ID')
    return getGroup(client, id, signal)
  },
  enabled: computed(() => groupId.value !== undefined),
  gcTime: 0,
})
</script>

<template>
  <div class="group-detail-page">
    <div
      v-if="groupId === undefined"
      class="group-detail-invalid"
      data-test="invalid-group-id"
      role="alert"
    >
      <h1>{{ t('group.invalidTitle') }}</h1>
      <p>{{ t('group.invalidDescription') }}</p>
      <RouterLink class="button-link" to="/">{{ t('shell.backHome') }}</RouterLink>
    </div>
    <template v-else>
      <QueryFeedback
        v-if="detailQuery.isPending.value"
        state="loading"
        :message="t('group.loading')"
      />
      <QueryFeedback
        v-else-if="detailQuery.isError.value && !detailQuery.data.value"
        state="error"
        :message="t('group.loadFailed')"
        :retry-label="t('common.retry')"
        @retry="detailQuery.refetch()"
      />
      <template v-else-if="detailQuery.data.value">
        <QueryFeedback
          v-if="detailQuery.isError.value"
          state="stale"
          :message="t('group.stale')"
          :retry-label="t('common.retry')"
          @retry="detailQuery.refetch()"
        />
        <GroupHeader :group="detailQuery.data.value" />
      </template>

      <GroupTabs />
      <GroupKeysTab v-if="activeTab === 'keys'" :key="groupId" :group-id="groupId" />
      <GroupModelsTab
        v-else-if="activeTab === 'models' && detailQuery.data.value"
        :key="groupId"
        :group-id="groupId"
        :group="detailQuery.data.value"
      />
      <SurfaceCard v-else-if="activeTab === 'settings'" class="group-detail-placeholder">
        <h2>{{ t('group.settingsTitle') }}</h2>
        <p>{{ t('group.settingsPending') }}</p>
      </SurfaceCard>
    </template>
  </div>
</template>

<style scoped>
.group-detail-page {
  display: grid;
  gap: var(--space-5);
}
.group-detail-invalid,
.group-detail-placeholder {
  display: grid;
  gap: var(--space-3);
}
.group-detail-invalid {
  max-width: 640px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  box-shadow: var(--shadow-card);
  padding: var(--space-6);
}
.group-detail-invalid h1,
.group-detail-placeholder h2 {
  max-width: none;
  margin: 0;
  font-size: 1.25rem;
}
.group-detail-invalid p,
.group-detail-placeholder p {
  margin: 0;
  color: var(--color-text-muted);
}
.group-detail-invalid .button-link {
  width: fit-content;
  margin-top: var(--space-2);
}
</style>
