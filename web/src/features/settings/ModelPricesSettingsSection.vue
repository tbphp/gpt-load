<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { modelPriceQueryOptions } from '@/app/resources/model-prices'
import { modelPricesLocation } from '@/app/route-locations'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import Surface from '@/components/ui/Surface.vue'
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
      <Surface variant="sunken" :padded="false" class="price-entry">
        <div class="price-entry__summary">
          <strong>
            {{
              t('settings.modelPrices.summary', {
                builtin: pricesQuery.data.value.builtin.length,
                overrides: pricesQuery.data.value.overrides.length,
                unit: t(`settings.modelPrices.units.${pricesQuery.data.value.price_unit}`),
              })
            }}
          </strong>
          <p>
            <template v-if="latestOverrideUpdatedAt !== null">
              {{
                t('settings.modelPrices.latestOverrideAt', {
                  time: formatLocalInstant(latestOverrideUpdatedAt, locale),
                })
              }}
            </template>
            {{ t('settings.modelPrices.historyNote') }}
          </p>
        </div>
        <RouterLink class="button-link" :to="modelPricesLocation()">
          {{ t('settings.modelPrices.manage') }}
        </RouterLink>
      </Surface>
    </template>
  </section>
</template>

<style scoped>
.settings-section {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading h2,
.settings-section__heading p,
.price-entry p {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--text-sm);
  font-weight: 650;
}

.settings-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.settings-section > .price-entry {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-5);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}

.price-entry__summary {
  min-width: 0;
}

.price-entry__summary strong {
  display: block;
  font-size: var(--text-label-xs);
  font-weight: 650;
}

.price-entry__summary p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

@media (max-width: 760px) {
  .settings-section > .price-entry {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
