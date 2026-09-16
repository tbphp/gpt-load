<script setup lang="ts">
import { computed } from 'vue'
import type { CredentialObservation } from '@modern/api/credential-observation'
import { AppBadge, AppOverflowText, type SemanticTone } from '@modern/components/ui'

const props = defineProps<{ name: string; level?: CredentialObservation['planLevel'] }>()
const tones: Record<NonNullable<CredentialObservation['planLevel']>, SemanticTone | 'brand'> = {
  free: 'neutral',
  standard: 'info',
  premium: 'brand',
  elite: 'warning',
}
const tone = computed(() => (props.level ? tones[props.level] : 'neutral'))
</script>

<template>
  <AppBadge :tone="tone" size="xs"><AppOverflowText :text="name" /></AppBadge>
</template>
