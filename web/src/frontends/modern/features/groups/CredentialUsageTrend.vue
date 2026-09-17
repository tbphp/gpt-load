<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getUsage, type UsageMetric } from '@modern/api/usage'
import { resolveTimeRange } from '@modern/app/time-range'
import { AppButton, AppCollectionState, AppSelect } from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import UsageTrend from '@modern/features/usage/UsageTrend.vue'
import { useApiClient } from '@shared/http/client-context'

const props = defineProps<{ group: number; credential: number }>()
const { t } = useI18n()
const client = useApiClient()
const metric = ref<UsageMetric>('tokens')
const metrics = computed(() =>
  (['tokens', 'requests', 'cost'] as const).map((value) => ({
    value,
    label: t('usage.' + value),
  })),
)
const query = useQuery(
  computed(() => ({
    queryKey: ['modern', 'credential-usage', props.group, props.credential],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getUsage(
        client,
        {
          ...resolveTimeRange({ preset: '7d' }),
          group_id: String(props.group),
          credential_id: String(props.credential),
        },
        signal,
      ),
  })),
)
useLoadingActivity(query.isFetching)
</script>

<template>
  <section class="modern-credential-usage-trend">
    <header>
      <h3>{{ t('credentialCards.localUsage') }}</h3>
      <AppSelect
        v-model="metric"
        :label="t('credentialCards.statisticsMetric')"
        label-hidden
        size="sm"
        :options="metrics"
      />
    </header>
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState
      v-else-if="query.isError.value"
      :title="t('credentialCards.statisticsFailed')"
      error
    >
      <AppButton size="sm" @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <UsageTrend v-else-if="query.data.value" :report="query.data.value" :metric="metric" compact />
  </section>
</template>

<style scoped>
.modern-credential-usage-trend {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
  padding-bottom: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-credential-usage-trend header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
}
.modern-credential-usage-trend h3 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
</style>
