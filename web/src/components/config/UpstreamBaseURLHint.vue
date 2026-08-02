<script setup lang="ts">
import { computed } from 'vue'

import type { GroupProtocol } from '@/api/control/types'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import { analyzeUpstreamBaseURL } from '@/lib/upstream-base-url'

const props = defineProps<{
  url: string
  protocols: readonly GroupProtocol[]
  missingMessage: string
  duplicateMessage: string
}>()

const message = computed(() => {
  const warning = analyzeUpstreamBaseURL(props.url, props.protocols)
  if (warning === 'missing-prefix') return props.missingMessage
  if (warning === 'duplicate-prefix') return props.duplicateMessage
  return ''
})
</script>

<template>
  <InlineFeedback v-if="message" tone="warning" appearance="hint">
    {{ message }}
  </InlineFeedback>
</template>
