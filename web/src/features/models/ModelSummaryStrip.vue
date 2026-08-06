<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelCollectionCatalogDto, ModelCollectionSummaryDto } from '@/app/resources/models'
import { modelPricesLocation } from '@/app/route-locations'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatInteger } from '@/lib/format'

const props = defineProps<{
  summary: ModelCollectionSummaryDto
  catalog: ModelCollectionCatalogDto
}>()
const { locale, t } = useI18n()

const stats = computed(() => [
  {
    key: 'clientModels',
    value: props.summary.client_model_count,
    tone: 'accent' as const,
  },
  {
    key: 'upstreamModels',
    value: props.summary.upstream_model_count,
    tone: 'plain' as const,
  },
  {
    key: 'prices',
    value: props.summary.price_count,
    tone: 'plain' as const,
  },
  {
    key: 'pendingPrices',
    value: props.summary.pending_price_count,
    tone: props.summary.pending_price_count > 0 ? ('warning' as const) : ('plain' as const),
  },
])

const catalogTone = computed(() => {
  if (!props.catalog.available) return 'neutral' as const
  return props.catalog.error_code ? ('warning' as const) : ('success' as const)
})

const catalogLabel = computed(() => {
  if (!props.catalog.available) return t('models.catalog.unavailable')
  return props.catalog.error_code ? t('models.catalog.stale') : t('models.catalog.available')
})
</script>

<template>
  <section class="models-summary" :aria-label="t('models.summary.label')">
    <dl class="models-summary__stats">
      <div v-for="stat in stats" :key="stat.key" :data-tone="stat.tone">
        <dt>{{ t(`models.summary.${stat.key}`) }}</dt>
        <dd>{{ formatInteger(stat.value, locale) }}</dd>
      </div>
    </dl>

    <div class="models-summary__catalog">
      <StatusBadge size="compact" :tone="catalogTone">{{ catalogLabel }}</StatusBadge>
      <p v-if="catalog.successful_fetch_at_ms > 0">
        <span>{{ t('models.catalog.lastSuccess') }}</span>
        <AppDateTime :instant="catalog.successful_fetch_at_ms" :locale="locale" />
      </p>
      <p v-if="catalog.error_code" class="models-summary__catalog-error">
        <code>{{ catalog.error_code }}</code>
        <template v-if="catalog.checked_at_ms > 0">
          <span>{{ t('models.catalog.lastCheck') }}</span>
          <AppDateTime :instant="catalog.checked_at_ms" :locale="locale" />
        </template>
      </p>
      <RouterLink
        v-if="summary.unreferenced_price_count > 0"
        class="models-summary__unreferenced"
        :to="modelPricesLocation()"
      >
        {{
          t('models.summary.unreferencedPrices', {
            count: formatInteger(summary.unreferenced_price_count, locale),
          })
        }}
      </RouterLink>
    </div>
  </section>
</template>

<style scoped>
.models-summary {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-4) var(--space-6);
}

.models-summary__stats {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  overflow: hidden;
  gap: 1px;
  margin: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-border-subtle);
}

.models-summary__stats > div {
  min-width: 0;
  background: var(--color-surface);
  padding: var(--space-3) var(--space-4);
}

.models-summary__stats dt {
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.models-summary__stats dd {
  margin: var(--space-1) 0 0;
  font-family: var(--font-mono);
  font-size: 22px;
  font-weight: 580;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.03em;
  line-height: 1.1;
}

.models-summary__stats > div[data-tone='accent'] dd {
  color: var(--color-action);
}

.models-summary__stats > div[data-tone='warning'] dd {
  color: var(--color-warning);
}

.models-summary__catalog {
  display: grid;
  justify-items: end;
  gap: var(--space-1);
}

.models-summary__catalog p {
  display: flex;
  align-items: baseline;
  gap: var(--space-1);
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.models-summary__catalog-error code {
  color: var(--color-warning);
  font-size: var(--text-label-xs);
}

.models-summary__unreferenced {
  color: var(--color-action);
  font-size: var(--text-label-xs);
  font-weight: 600;
}

@media (max-width: 980px) {
  .models-summary {
    grid-template-columns: minmax(0, 1fr);
  }

  .models-summary__catalog {
    justify-items: start;
  }
}

@media (max-width: 620px) {
  .models-summary__stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
