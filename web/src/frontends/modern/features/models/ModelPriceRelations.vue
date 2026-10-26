<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import {
  getPriceSources,
  priceSourcesKey,
  type ModelPrice,
  type ModelPriceSource,
} from '@modern/api/models'
import { AppButton, AppNotice, AppOverflowText } from '@modern/components/ui'
import ModelPriceSummary from './ModelPriceSummary.vue'

const props = defineProps<{ price: ModelPrice }>()
const emit = defineEmits<{ inspect: [model: string, id: number] }>()
const { t, n } = useI18n()
const client = useApiClient()
const query = useQuery({
  queryKey: priceSourcesKey,
  queryFn: ({ signal }) => getPriceSources(client, signal),
})
const items = computed(() => query.data.value ?? [])
const source = computed(() =>
  items.value.find((item) => item.price.id === props.price.sourcePriceID),
)
const children = computed(() =>
  items.value.filter((item) => item.price.sourcePriceID === props.price.id),
)
const expanded = ref(false)
const cached = computed(() => {
  let price = props.price
  const seen = new Set<number>()
  while (price.sourcePriceID && !seen.has(price.id)) {
    seen.add(price.id)
    const next = items.value.find((item) => item.price.id === price.sourcePriceID)
    if (!next) return false
    price = next.price
  }
  return price.method === 'auto_sync' && price.provider === null && price.status === 'configured'
})
function inspect(item: ModelPriceSource): void {
  emit('inspect', item.aliases[0] || item.price.model, item.price.id)
}
</script>

<template>
  <div
    v-if="
      query.isPending.value ||
      query.isError.value ||
      price.sourcePriceID ||
      children.length ||
      cached
    "
    class="modern-price-relations"
  >
    <p v-if="query.isPending.value" class="modern-price-relations-loading" role="status">
      {{ t('ui.loading') }}
    </p>
    <AppNotice v-if="query.isError.value" tone="warning">
      {{ t('modelManager.reuse.relationsFailed') }}
      <template #actions
        ><AppButton
          variant="text"
          size="xs"
          :disabled="query.isFetching.value"
          @click="query.refetch()"
          >{{ t('ui.retry') }}</AppButton
        ></template
      >
    </AppNotice>
    <AppButton v-if="source" variant="text" size="sm" @click="inspect(source)">{{
      t('modelManager.reuse.linkedTo', {
        provider: source.price.channel.name,
        model: source.price.model,
      })
    }}</AppButton>
    <AppNotice v-if="cached" tone="warning">{{ t('modelManager.reuse.cached') }}</AppNotice>
    <AppButton
      v-if="children.length"
      variant="text"
      size="sm"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
      >{{ t('modelManager.reuse.dependents', { count: n(children.length) }) }}</AppButton
    >
    <div v-if="expanded" class="modern-price-relations-list">
      <AppButton
        v-for="item in children"
        :key="item.price.id"
        variant="ghost"
        class="modern-price-relation"
        @click="inspect(item)"
      >
        <AppOverflowText :text="item.price.channel.name + ' · ' + item.price.model" />
        <ModelPriceSummary :prices="item.price.prices" />
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.modern-price-relations {
  display: grid;
  gap: var(--modern-space-2);
}
.modern-price-relations-loading {
  margin: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-price-relations-list {
  display: grid;
  gap: var(--modern-space-1);
}
.modern-price-relation {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-2);
  text-align: left;
}
</style>
