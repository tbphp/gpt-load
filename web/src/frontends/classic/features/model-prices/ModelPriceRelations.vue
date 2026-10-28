<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import {
  getModelPriceSources,
  modelPriceSourcesKey,
  type ModelPriceDto,
} from '@/app/resources/model-prices'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import ModelPriceSummary from './ModelPriceSummary.vue'

const props = defineProps<{ price: ModelPriceDto }>()
const emit = defineEmits<{ inspect: [id: number] }>()
const { t } = useI18n()
const client = useApiClient()
const query = useQuery({
  queryKey: modelPriceSourcesKey,
  queryFn: ({ signal }) => getModelPriceSources(client, signal),
})
const items = computed(() => query.data.value ?? [])
const source = computed(() =>
  items.value.find((item) => item.price.id === props.price.source_price_id),
)
const children = computed(() =>
  items.value.filter((item) => item.price.source_price_id === props.price.id),
)
const expanded = ref(false)
const cached = computed(() => {
  let price = props.price
  const seen = new Set<number>()
  while (price.source_price_id && !seen.has(price.id)) {
    seen.add(price.id)
    const next = items.value.find((item) => item.price.id === price.source_price_id)
    if (!next) return false
    price = next.price
  }
  return (
    price.method === 'auto_sync' &&
    price.matched_provider_id === null &&
    price.pricing_status === 'configured'
  )
})
</script>

<template>
  <div
    v-if="
      query.isPending.value ||
      query.isError.value ||
      price.source_price_id ||
      children.length ||
      cached
    "
    class="price-relations"
  >
    <p v-if="query.isPending.value" class="price-relations-loading" role="status">
      {{ t('modelPrices.reuse.loading') }}
    </p>
    <InlineFeedback v-if="query.isError.value" tone="warning">
      {{ t('modelPrices.reuse.relationsFailed') }}
      <template #action
        ><AppButton
          variant="link"
          size="xs"
          :disabled="query.isFetching.value"
          @click="query.refetch()"
          >{{ t('common.retry') }}</AppButton
        ></template
      >
    </InlineFeedback>
    <AppButton
      v-if="source"
      class="price-relations-source"
      variant="link"
      size="compact"
      @click="emit('inspect', source.price.id)"
      >{{
        t('modelPrices.reuse.linkedTo', {
          provider: source.price.channel_name,
          model: source.price.model_id,
        })
      }}</AppButton
    >
    <InlineFeedback v-if="cached" tone="warning">{{
      t('modelPrices.reuse.cached')
    }}</InlineFeedback>
    <AppButton
      v-if="children.length"
      variant="link"
      size="compact"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
      >{{ t('modelPrices.reuse.dependents', { count: children.length }) }}</AppButton
    >
    <div v-if="expanded" class="price-relations-list">
      <AppButton
        v-for="item in children"
        :key="item.price.id"
        variant="ghost"
        size="compact"
        class="price-relation"
        @click="emit('inspect', item.price.id)"
      >
        <span>{{ item.price.channel_name }} · {{ item.price.model_id }}</span
        ><ModelPriceSummary :prices="item.price.prices" />
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.price-relations {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}
.price-relations-source {
  min-width: 0;
  justify-content: flex-start;
  overflow-wrap: anywhere;
  white-space: normal;
  text-align: left;
}
.price-relations-loading {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.price-relations-list {
  display: grid;
  gap: var(--space-1);
}
.price-relation {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-2);
  text-align: left;
}
.price-relation > :first-child {
  min-width: 0;
  overflow-wrap: anywhere;
  white-space: normal;
}
</style>
