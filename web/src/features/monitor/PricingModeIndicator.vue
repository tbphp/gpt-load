<script setup lang="ts">
import { Zap } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import AppTooltip from '@/components/ui/AppTooltip.vue'

defineProps<{
  mode: string | null
}>()

const { t } = useI18n()
</script>

<template>
  <AppTooltip v-if="mode === 'fast'" :content="t('monitor.logs.pricingMode.fastTooltip')">
    <span
      class="pricing-mode-indicator"
      tabindex="0"
      :aria-label="t('monitor.logs.pricingMode.fastLabel')"
    >
      <Zap :size="13" :stroke-width="2.25" aria-hidden="true" />
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
  border: 1px solid color-mix(in srgb, var(--color-info) 26%, var(--color-border-subtle));
  border-radius: var(--radius-tag);
  background: var(--color-info-bg);
  color: var(--color-info);
  cursor: help;
}

.pricing-mode-indicator:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
</style>
