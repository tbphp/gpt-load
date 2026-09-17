<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCredentialQuotaHistory } from '@modern/api/credential-quota-history'
import { resolveTimeRange } from '@modern/app/time-range'
import { AppButton, AppCollectionState } from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import CredentialQuotaTrend from './CredentialQuotaTrend.vue'

const props = defineProps<{ group: number; credential: number }>()
const { t } = useI18n()
const client = useApiClient()
const query = useQuery(
  computed(() => ({
    queryKey: ['modern', 'credential-quota-history', props.group, props.credential],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getCredentialQuotaHistory(
        client,
        props.group,
        props.credential,
        resolveTimeRange({ preset: '7d' }),
        signal,
      ),
  })),
)
const report = computed(() => query.data.value)
const visible = computed(
  () =>
    query.isPending.value ||
    query.isError.value ||
    report.value?.windows.some((window) => window.points.length),
)
useLoadingActivity(query.isFetching)
</script>
<template>
  <section v-if="visible" class="modern-credential-quota-history">
    <h3>{{ t('credentialCards.quotaHistory') }}</h3>
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState
      v-else-if="query.isError.value"
      :title="t('credentialCards.quotaHistoryFailed')"
      error
      ><AppButton size="sm" @click="query.refetch()">{{
        t('ui.retry')
      }}</AppButton></AppCollectionState
    >
    <CredentialQuotaTrend v-else-if="report" :report="report" />
  </section>
</template>
<style scoped>
.modern-credential-quota-history {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
  padding-bottom: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-credential-quota-history h3 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
</style>
