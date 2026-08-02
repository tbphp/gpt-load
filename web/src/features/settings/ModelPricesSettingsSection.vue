<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { modelPriceQueryOptions } from '@/app/resources/model-prices'
import { modelPricesLocation } from '@/app/route-locations'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import { formatLocalInstant } from '@/lib/format'

const client = useApiClient()
const { locale, t } = useI18n()
const pricesQuery = useQuery(modelPriceQueryOptions(client))
const latestOverrideUpdatedAt = computed(() => {
  const overrides = pricesQuery.data.value?.overrides ?? []
  if (overrides.length === 0) return null
  return Math.max(...overrides.map((rule) => rule.updated_at_ms))
})
</script>

<template>
  <section id="settings-model-prices" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.modelPrices.title') }}</h2>
      <p>{{ t('settings.modelPrices.description') }}</p>
    </header>

    <QueryFeedback
      v-if="pricesQuery.isPending.value"
      state="loading"
      :message="t('settings.modelPrices.loading')"
    />
    <QueryFeedback
      v-else-if="pricesQuery.isError.value && !pricesQuery.data.value"
      state="error"
      :message="t('settings.modelPrices.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="pricesQuery.refetch()"
    />
    <template v-else-if="pricesQuery.data.value">
      <QueryFeedback
        v-if="pricesQuery.isError.value"
        state="stale"
        :message="t('settings.modelPrices.stale')"
        :retry-label="t('common.retry')"
        @retry="pricesQuery.refetch()"
      />
      <dl class="settings-model-prices__summary">
        <div>
          <dt>{{ t('settings.modelPrices.builtinCount') }}</dt>
          <dd>{{ pricesQuery.data.value.builtin.length }}</dd>
        </div>
        <div>
          <dt>{{ t('settings.modelPrices.overrideCount') }}</dt>
          <dd>{{ pricesQuery.data.value.overrides.length }}</dd>
        </div>
        <div>
          <dt>{{ t('settings.modelPrices.priceUnit') }}</dt>
          <dd>{{ t(`settings.modelPrices.units.${pricesQuery.data.value.price_unit}`) }}</dd>
        </div>
        <div v-if="latestOverrideUpdatedAt !== null">
          <dt>{{ t('settings.modelPrices.latestOverride') }}</dt>
          <dd>{{ formatLocalInstant(latestOverrideUpdatedAt, locale) }}</dd>
        </div>
      </dl>
      <p class="settings-model-prices__note">{{ t('settings.modelPrices.historyNote') }}</p>
      <RouterLink class="settings-model-prices__manage" :to="modelPricesLocation()">
        {{ t('settings.modelPrices.manage') }}
      </RouterLink>
    </template>
  </section>
</template>

<style scoped>
.settings-section,
.settings-model-prices__summary,
.settings-model-prices__summary > div {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading h2,
.settings-section__heading p,
.settings-model-prices__summary,
.settings-model-prices__summary dd,
.settings-model-prices__note {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--text-sm);
  font-weight: 650;
}

.settings-section__heading p,
.settings-model-prices__note {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.settings-model-prices__summary {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}

.settings-model-prices__summary > div {
  gap: var(--space-1);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}

.settings-model-prices__summary dt {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.settings-model-prices__summary dd {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: 560;
}

.settings-model-prices__manage {
  width: fit-content;
  color: var(--color-action);
  font-size: var(--text-sm);
  font-weight: 650;
  text-decoration: underline;
  text-underline-offset: 3px;
}

@media (max-width: 640px) {
  .settings-model-prices__summary {
    grid-template-columns: 1fr;
  }
}
</style>
