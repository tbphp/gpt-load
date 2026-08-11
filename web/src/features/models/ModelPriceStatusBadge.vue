<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelPriceDto } from '@/app/resources/model-prices'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import type { StatusIcon, StatusTone } from '@/components/ui/status-presenter'

const props = defineProps<{
  price: Pick<ModelPriceDto, 'method' | 'pricing_status' | 'matched_provider_id' | 'match_source'>
  providerName?: string
}>()
const { t } = useI18n()

const presentation = computed<{ icon: StatusIcon; label: string; tone: StatusTone }>(() => {
  switch (props.price.method) {
    case 'auto_sync':
      return props.price.match_source === 'provider_priority_fallback'
        ? { icon: 'alert', label: t('modelPrices.method.reference_price'), tone: 'warning' }
        : { icon: 'check', label: t('modelPrices.method.auto_sync'), tone: 'success' }
    case 'user_set':
      return { icon: 'edit', label: t('modelPrices.method.user_set'), tone: 'neutral' }
    case 'user_marked_unpriced':
      return { icon: 'off', label: t('modelPrices.status.unpriced'), tone: 'neutral' }
    default:
      return props.price.pricing_status === 'pending'
        ? { icon: 'alert', label: t('modelPrices.status.pending'), tone: 'warning' }
        : { icon: 'check', label: t('modelPrices.status.configured'), tone: 'success' }
  }
})

const sourceDetail = computed(() => {
  if (props.price.method !== 'auto_sync' || props.price.match_source === null) return null
  const provider = props.providerName?.trim() || props.price.matched_provider_id
  if (!provider) return null
  return t(`modelPrices.source.${props.price.match_source}`, { provider })
})
</script>

<template>
  <AppTooltip :content="sourceDetail ?? ''" :disabled="sourceDetail === null">
    <span class="model-price-status" :tabindex="sourceDetail === null ? undefined : 0">
      <StatusBadge :tone="presentation.tone" :icon="presentation.icon" size="compact">
        {{ presentation.label }}
      </StatusBadge>
    </span>
  </AppTooltip>
</template>

<style scoped>
.model-price-status {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-1);
  outline: none;
}

.model-price-status:focus-visible {
  border-radius: var(--radius-control);
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
</style>
