<script setup lang="ts">
import { ArrowRightLeft } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { protocolLabelKey } from '@/api/control/protocols'
import type { AccessProtocol } from '@/api/control/types'
import type { RequestLogRouteMode } from '@/app/resources/request-logs'
import AppTooltip from '@/components/ui/AppTooltip.vue'

const props = defineProps<{
  mode: RequestLogRouteMode | null
  clientProtocol: AccessProtocol
  upstreamProtocol: string | null
}>()

const { t } = useI18n()
const clientProtocolLabel = computed(() => t(protocolLabelKey(props.clientProtocol)))
const upstreamProtocolLabel = computed(() => props.upstreamProtocol?.trim() || '—')
const tooltip = computed(() =>
  t('monitor.logs.protocolConversion.tooltip', {
    client: clientProtocolLabel.value,
    upstream: upstreamProtocolLabel.value,
  }),
)
const label = computed(() =>
  t('monitor.logs.protocolConversion.label', {
    client: clientProtocolLabel.value,
    upstream: upstreamProtocolLabel.value,
  }),
)
</script>

<template>
  <AppTooltip v-if="mode === 'converted'" :content="tooltip">
    <span class="log-protocol-conversion" tabindex="0" :aria-label="label">
      <ArrowRightLeft :size="14" :stroke-width="1.8" aria-hidden="true" />
    </span>
  </AppTooltip>
</template>

<style scoped>
.log-protocol-conversion {
  display: inline-flex;
  width: 20px;
  height: 20px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-tag);
  color: var(--color-warning);
  cursor: help;
}

.log-protocol-conversion:hover {
  background: color-mix(in srgb, var(--color-warning) 10%, transparent);
}

.log-protocol-conversion:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
</style>
