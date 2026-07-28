<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { CircleAlert, CircleCheck, CircleOff, Layers3 } from 'lucide-vue-next'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { listAccessKeyOptions } from '@/app/resources/access-keys'
import { listGroups } from '@/app/resources/groups'
import { getRuntimeHealth } from '@/app/resources/health'
import { NetworkError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import { controlQueryKeys } from '@/app/query-keys'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import ConnectionCard from './ConnectionCard.vue'
import GroupCard from './GroupCard.vue'
import UsageSummaryCard from './UsageSummaryCard.vue'

const props = withDefaults(defineProps<{ origin?: string }>(), {
  origin: () => window.location.origin,
})
const client = useApiClient()
const { locale, t } = useI18n()

const groupsQuery = useQuery({
  queryKey: controlQueryKeys.groups.list(),
  queryFn: ({ signal }) => listGroups(client, signal),
})
const healthQuery = useQuery({
  queryKey: controlQueryKeys.health(),
  queryFn: ({ signal }) => getRuntimeHealth(client, signal),
})
const accessKeysQuery = useQuery({
  queryKey: controlQueryKeys.accessKeys.options(),
  queryFn: ({ signal }) => listAccessKeyOptions(client, signal),
  gcTime: 0,
})

const healthByGroup = computed(
  () => new Map(healthQuery.data.value?.groups.map((group) => [group.id, group]) ?? []),
)
const modelIds = computed(() =>
  (groupsQuery.data.value ?? []).flatMap((group) => group.models.map((model) => model.id)),
)
const healthErrorMessage = computed(() =>
  healthQuery.error.value instanceof NetworkError
    ? t('home.networkUnavailable')
    : t('home.healthUnavailable'),
)
</script>

<template>
  <div class="home-page">
    <PageHeader :title="t('home.title')" :description="t('home.description')" />

    <section
      class="operational-overview"
      data-test="home-operational-overview"
      aria-labelledby="operational-overview-heading"
    >
      <header class="operational-overview__header">
        <h2 id="operational-overview-heading">{{ t('home.operationalOverview') }}</h2>
        <p>{{ t('home.operationalOverviewDescription') }}</p>
      </header>

      <div class="home-overview">
        <SurfaceCard class="service-card">
          <div class="section-heading">
            <div>
              <p class="eyebrow">{{ t('home.service') }}</p>
              <h3 id="service-heading">{{ t('home.service') }}</h3>
            </div>
            <span
              v-if="healthQuery.isSuccess.value"
              class="service-status service-status--normal"
              data-test="home-service-status"
            >
              <CircleCheck :size="14" aria-hidden="true" />
              {{ t('home.online') }}
            </span>
            <StatusBadge
              v-else-if="healthQuery.isError.value && healthQuery.data.value"
              tone="warning"
            >
              {{ t('home.healthStale') }}
            </StatusBadge>
            <StatusBadge v-else-if="healthQuery.isError.value" tone="warning">
              {{ healthErrorMessage }}
            </StatusBadge>
          </div>

          <QueryFeedback
            v-if="healthQuery.isPending.value"
            state="loading"
            :message="t('auth.checking')"
          />
          <QueryFeedback
            v-else-if="healthQuery.isError.value && !healthQuery.data.value"
            state="error"
            :message="healthErrorMessage"
            :retry-label="t('common.retry')"
            @retry="healthQuery.refetch()"
          />
          <template v-else-if="healthQuery.data.value">
            <QueryFeedback
              v-if="healthQuery.isError.value"
              state="stale"
              :message="t('home.healthStale')"
              :retry-label="t('common.retry')"
              @retry="healthQuery.refetch()"
            />
            <div class="health-meta">
              <span>{{
                t('home.revision', { revision: healthQuery.data.value.snapshot_revision })
              }}</span>
              <i18n-t keypath="home.observedAt" tag="span">
                <template #time>
                  <AppDateTime :instant="healthQuery.data.value.observed_at" :locale="locale" />
                </template>
              </i18n-t>
            </div>
            <div class="health-counts">
              <span data-test="home-health-total" data-state="normal">{{
                t('home.keyTotal', { count: healthQuery.data.value.counts.total })
              }}</span>
              <span data-test="home-health-available" data-state="normal">{{
                t('home.keyAvailable', { count: healthQuery.data.value.counts.available })
              }}</span>
              <span
                data-test="home-health-cooldown"
                :data-state="healthQuery.data.value.counts.cooldown > 0 ? 'anomaly' : 'normal'"
                :class="{
                  'health-count--warning': healthQuery.data.value.counts.cooldown > 0,
                }"
              >
                <CircleAlert
                  v-if="healthQuery.data.value.counts.cooldown > 0"
                  :size="14"
                  aria-hidden="true"
                />
                {{ t('home.keyCooldown', { count: healthQuery.data.value.counts.cooldown }) }}
              </span>
              <span
                data-test="home-health-blacklisted"
                :data-state="healthQuery.data.value.counts.blacklisted > 0 ? 'anomaly' : 'normal'"
                :class="{
                  'health-count--danger': healthQuery.data.value.counts.blacklisted > 0,
                }"
              >
                <CircleOff
                  v-if="healthQuery.data.value.counts.blacklisted > 0"
                  :size="14"
                  aria-hidden="true"
                />
                {{ t('home.keyBlacklisted', { count: healthQuery.data.value.counts.blacklisted }) }}
              </span>
              <span
                data-test="home-health-disabled"
                :data-state="healthQuery.data.value.counts.disabled > 0 ? 'anomaly' : 'normal'"
                :class="{
                  'health-count--warning': healthQuery.data.value.counts.disabled > 0,
                }"
              >
                <CircleOff
                  v-if="healthQuery.data.value.counts.disabled > 0"
                  :size="14"
                  aria-hidden="true"
                />
                {{ t('home.keyDisabled', { count: healthQuery.data.value.counts.disabled }) }}
              </span>
            </div>
          </template>
        </SurfaceCard>

        <UsageSummaryCard heading-as="h3" />
      </div>
    </section>

    <section class="groups-section" data-test="home-groups" aria-labelledby="groups-heading">
      <header class="groups-section__header">
        <div>
          <h2 id="groups-heading">{{ t('home.groups') }}</h2>
          <p>{{ t('home.groupsDescription') }}</p>
        </div>
      </header>

      <QueryFeedback
        v-if="groupsQuery.isPending.value"
        state="loading"
        :message="t('home.groupsLoading')"
      />
      <QueryFeedback
        v-else-if="groupsQuery.isError.value && !groupsQuery.data.value"
        state="error"
        :message="t('home.groupsError')"
        :retry-label="t('common.retry')"
        @retry="groupsQuery.refetch()"
      />
      <template v-else>
        <QueryFeedback
          v-if="groupsQuery.isError.value"
          state="stale"
          :message="t('home.groupsStale')"
          :retry-label="t('common.retry')"
          @retry="groupsQuery.refetch()"
        />
        <EmptyState
          v-if="groupsQuery.data.value?.length === 0"
          :title="t('home.noGroupsTitle')"
          :description="t('home.noGroupsDescription')"
        >
          <template #icon><Layers3 :size="32" /></template>
          <template #actions>
            <RouterLink class="button-link" to="/import">{{ t('home.importKeys') }}</RouterLink>
          </template>
        </EmptyState>
        <div v-else class="group-grid">
          <GroupCard
            v-for="group in groupsQuery.data.value"
            :key="group.id"
            :group="group"
            :health="healthByGroup.get(group.id)"
          />
        </div>
      </template>
    </section>

    <section
      class="connection-section"
      data-test="home-connection"
      :aria-label="t('home.connection')"
    >
      <QueryFeedback
        v-if="accessKeysQuery.isPending.value"
        state="loading"
        :message="t('auth.checking')"
      />
      <QueryFeedback
        v-else-if="accessKeysQuery.isError.value && !accessKeysQuery.data.value"
        state="error"
        :message="t('home.accessKeysError')"
        :retry-label="t('common.retry')"
        @retry="accessKeysQuery.refetch()"
      />
      <template v-else>
        <QueryFeedback
          v-if="accessKeysQuery.isError.value"
          state="stale"
          :message="t('home.accessKeysStale')"
          :retry-label="t('common.retry')"
          @retry="accessKeysQuery.refetch()"
        />
        <ConnectionCard
          :keys="accessKeysQuery.data.value ?? []"
          :model-ids="modelIds"
          :origin="props.origin"
        />
      </template>
    </section>
  </div>
</template>

<style scoped>
.home-page {
  display: grid;
  gap: var(--space-8);
}
.operational-overview,
.connection-section {
  display: grid;
  gap: var(--space-4);
}
.operational-overview__header h2,
.operational-overview__header p {
  margin: 0;
}
.operational-overview__header h2 {
  font-size: 1.25rem;
}
.operational-overview__header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.home-overview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(320px, 0.9fr);
  gap: var(--space-5);
  align-items: start;
}
.service-card {
  display: grid;
  gap: var(--space-5);
}
.service-card {
  padding: var(--space-5);
}
.section-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.section-heading h3,
.groups-section h2 {
  margin: 0;
  font-size: 1.125rem;
}
.service-status {
  display: inline-flex;
  min-height: 28px;
  align-items: center;
  gap: var(--space-1);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: var(--space-1) var(--space-2);
  font-size: var(--text-sm);
  font-weight: 650;
}
.health-meta,
.health-counts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-4);
}
.health-meta {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
.health-counts span {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  border-radius: var(--radius-tag);
  background: var(--color-surface-secondary);
  padding: var(--space-2) var(--space-3);
}
.health-counts .health-count--warning {
  background: var(--color-warning-bg);
}
.health-counts .health-count--warning svg {
  color: var(--color-warning);
}
.health-counts .health-count--danger {
  background: var(--color-danger-bg);
}
.health-counts .health-count--danger svg {
  color: var(--color-danger);
}
.groups-section {
  display: grid;
  gap: var(--space-4);
}
.groups-section__header p {
  max-width: 68ch;
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}
.group-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}
@media (max-width: 900px) {
  .home-overview,
  .group-grid {
    grid-template-columns: 1fr;
  }
}
</style>
