<script setup lang="ts">
import type { ModelPricingStatus } from '@/api/control/types'
import StatusBadge from '@/components/ui/StatusBadge.vue'

defineProps<{
  status: ModelPricingStatus
  labels: Record<ModelPricingStatus, string>
}>()
</script>

<template>
  <StatusBadge :tone="status === 'configured' ? 'success' : 'warning'" size="compact">
    {{ labels[status] }}
  </StatusBadge>
</template>
