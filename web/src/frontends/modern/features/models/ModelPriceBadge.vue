<script setup lang="ts">
import { CircleDollarSign, CircleHelp } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppBadge, AppTooltip } from '@modern/components/ui'
import type { ModelCandidate } from '@modern/api/model-discovery'

const props = defineProps<{ candidate?: Pick<ModelCandidate, 'pricingStatus' | 'pricingSource'> }>()
const { t } = useI18n()
const priced = computed(() => props.candidate?.pricingStatus === 'configured')
const label = computed(() =>
  t(
    !props.candidate
      ? 'modelSelection.prices.unknown'
      : !priced.value
        ? 'modelSelection.prices.pending'
        : props.candidate.pricingSource
          ? 'modelSelection.prices.matched'
          : 'modelSelection.prices.configured',
  ),
)
const detail = computed(() =>
  props.candidate?.pricingSource
    ? t('modelSelection.priceSource', { source: props.candidate.pricingSource })
    : '',
)
</script>

<template>
  <AppTooltip :label="detail">
    <AppBadge
      :icon="priced ? CircleDollarSign : CircleHelp"
      :tone="priced ? 'success' : 'neutral'"
      variant="plain"
      :tabindex="detail ? 0 : undefined"
      >{{ label }}</AppBadge
    >
  </AppTooltip>
</template>
