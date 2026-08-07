<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelPriceDto } from '@/app/resources/model-prices'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import type { StatusIcon, StatusTone } from '@/components/ui/status-presenter'

const props = defineProps<{
  price: Pick<ModelPriceDto, 'method' | 'pricing_status'>
}>()
const { t } = useI18n()

const presentation = computed<{ icon: StatusIcon; label: string; tone: StatusTone }>(() => {
  switch (props.price.method) {
    case 'auto_sync':
      return { icon: 'check', label: t('modelPrices.method.auto_sync'), tone: 'success' }
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
</script>

<template>
  <StatusBadge :tone="presentation.tone" :icon="presentation.icon" size="compact">
    {{ presentation.label }}
  </StatusBadge>
</template>
