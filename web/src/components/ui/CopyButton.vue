<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'
import { onBeforeUnmount, ref } from 'vue'

import { copyText } from '@/lib/clipboard'

const props = defineProps<{
  value: string
  label: string
  successLabel: string
  failureLabel: string
}>()
const state = ref<'idle' | 'success' | 'failure'>('idle')
let resetTimer: number | undefined

async function copy(): Promise<void> {
  try {
    await copyText(props.value)
    state.value = 'success'
  } catch {
    state.value = 'failure'
  }
  window.clearTimeout(resetTimer)
  resetTimer = window.setTimeout(() => (state.value = 'idle'), 2000)
}

onBeforeUnmount(() => window.clearTimeout(resetTimer))
</script>

<template>
  <span class="copy-control">
    <button type="button" :aria-label="label" @click="copy">
      <Check v-if="state === 'success'" :size="16" aria-hidden="true" />
      <Copy v-else :size="16" aria-hidden="true" />
    </button>
    <span
      v-if="state !== 'idle'"
      class="copy-control__feedback"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      {{ state === 'success' ? successLabel : failureLabel }}
    </span>
  </span>
</template>

<style scoped>
.copy-control {
  position: relative;
  display: inline-flex;
}
.copy-control button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  cursor: pointer;
}
.copy-control__feedback {
  position: absolute;
  z-index: var(--z-popover);
  top: calc(100% + var(--space-1));
  right: 0;
  width: max-content;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-1) var(--space-2);
  box-shadow: var(--shadow-card);
  font-size: 0.75rem;
}
</style>
