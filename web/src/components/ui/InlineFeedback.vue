<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    tone?: 'info' | 'warning' | 'danger'
  }>(),
  {
    tone: 'info',
  },
)

const role = computed(() => (props.tone === 'info' ? 'status' : 'alert'))
const glyph = computed(() => {
  if (props.tone === 'danger') return '▲'
  if (props.tone === 'warning') return '▲'
  return 'i'
})
</script>

<template>
  <div class="inline-feedback" :class="`inline-feedback--${tone}`" :role="role">
    <span class="inline-feedback__glyph" aria-hidden="true">{{ glyph }}</span>
    <span><slot /></span>
  </div>
</template>
