<script setup lang="ts">
import { ChartNoAxesColumnIncreasing, Zap } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppTooltip from '@/components/ui/AppTooltip.vue'

import { formatLogTokenCount } from './log-format'

const props = defineProps<{
  mode: string | null
  contextThresholdTokens: string | null
}>()

const { locale, t } = useI18n()
const tierLabel = computed(() =>
  props.contextThresholdTokens === null
    ? ''
    : t('monitor.logs.pricingMode.tierLabel', {
        threshold: formatLogTokenCount(props.contextThresholdTokens, locale.value),
      }),
)
</script>

<template>
  <AppTooltip v-if="contextThresholdTokens !== null" :content="tierLabel">
    <span class="pricing-mode-indicator" tabindex="0" :aria-label="tierLabel">
      <ChartNoAxesColumnIncreasing :size="13" aria-hidden="true" />
    </span>
  </AppTooltip>
  <AppTooltip v-else-if="mode === 'fast'" :content="t('monitor.logs.pricingMode.fastLabel')">
    <span
      class="pricing-mode-indicator"
      tabindex="0"
      :aria-label="t('monitor.logs.pricingMode.fastLabel')"
    >
      <Zap :size="13" aria-hidden="true" />
    </span>
  </AppTooltip>
</template>

<style scoped>
.pricing-mode-indicator {
  display: inline-flex;
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-tag);
  background: transparent;
  color: var(--color-text-faint);
  padding: 0;
  cursor: help;
}

.pricing-mode-indicator:hover {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.pricing-mode-indicator:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
</style>
