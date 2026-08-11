<script setup lang="ts">
import { ArrowRightLeft } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { protocolLabelKey, upstreamAPILabelKey } from '@/api/control/protocols'
import type { AccessProtocol } from '@/api/control/types'
import type { RequestLogRouteMode, RequestLogUpstreamAPI } from '@/app/resources/request-logs'
import AppTooltip from '@/components/ui/AppTooltip.vue'

const props = defineProps<{
  mode: RequestLogRouteMode | null
  clientProtocol: AccessProtocol
  upstreamApi: RequestLogUpstreamAPI | null
}>()

const { t } = useI18n()
const clientProtocolLabel = computed(() => t(protocolLabelKey(props.clientProtocol)))
const upstreamAPILabel = computed(() =>
  props.upstreamApi === null
    ? t('monitor.logs.protocolConversion.notRecorded')
    : t(upstreamAPILabelKey(props.upstreamApi)),
)
const tooltip = computed(() => {
  if (props.upstreamApi === null) {
    return t('monitor.logs.protocolConversion.tooltipNotRecorded', {
      client: clientProtocolLabel.value,
    })
  }
  return t('monitor.logs.protocolConversion.tooltip', {
    client: clientProtocolLabel.value,
    upstream: upstreamAPILabel.value,
  })
})
const label = computed(() =>
  t('monitor.logs.protocolConversion.label', {
    client: clientProtocolLabel.value,
    upstream: upstreamAPILabel.value,
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
