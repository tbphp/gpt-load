<script setup lang="ts">
import { Copy } from '@lucide/vue'
import { onBeforeUnmount, ref } from 'vue'

const props = defineProps<{
  value: string
  label: string
  successLabel: string
  failureLabel: string
}>()

type CopyState = 'idle' | 'success' | 'failure'

const state = ref<CopyState>('idle')
let resetTimer: ReturnType<typeof setTimeout> | undefined

function scheduleReset(): void {
  if (resetTimer !== undefined) clearTimeout(resetTimer)
  resetTimer = setTimeout(() => {
    state.value = 'idle'
    resetTimer = undefined
  }, 2_000)
}

async function copyValue(): Promise<void> {
  try {
    if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable')
    await navigator.clipboard.writeText(props.value)
    state.value = 'success'
  } catch {
    state.value = 'failure'
  }
  scheduleReset()
}

onBeforeUnmount(() => {
  if (resetTimer !== undefined) clearTimeout(resetTimer)
})
</script>

<template>
  <span class="copy-chip-wrap">
    <button
      class="copy-chip"
      :data-state="state"
      type="button"
      :aria-label="label"
      @click="copyValue"
    >
      <Copy :size="14" aria-hidden="true" />
      <span>{{ value }}</span>
    </button>
    <span
      v-if="state !== 'idle'"
      class="copy-chip__feedback"
      :class="`copy-chip__feedback--${state}`"
      role="status"
      aria-live="polite"
    >
      {{ state === 'success' ? successLabel : failureLabel }}
    </span>
  </span>
</template>

<style scoped>
.copy-chip-wrap {
  position: relative;
  display: inline-flex;
  width: auto;
  max-width: 100%;
  min-width: 0;
}

.copy-chip {
  display: inline-flex;
  width: auto;
  max-width: 100%;
  min-width: 0;
  min-height: var(--control-compact);
  align-items: center;
  justify-content: flex-start;
  gap: 7px;
  border: 0;
  border-radius: var(--radius-tag);
  appearance: none;
  background: transparent;
  color: var(--color-text-faint);
  padding: 3px 0;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  cursor: pointer;
  transition:
    color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.copy-chip span {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.copy-chip:hover,
.copy-chip[data-state='success'],
.copy-chip[data-state='failure'] {
  background: var(--color-surface-sunken);
  color: var(--color-action);
}

.copy-chip[data-state='success'] {
  color: var(--color-success);
}

.copy-chip[data-state='failure'] {
  color: var(--color-danger);
}

.copy-chip__feedback {
  position: absolute;
  z-index: var(--z-popover);
  top: calc(100% + 7px);
  left: 0;
  border: 1px solid var(--color-feedback-success-border);
  border-radius: var(--radius-tag);
  background: var(--color-surface);
  box-shadow: var(--shadow-feedback);
  color: var(--color-success);
  padding: 5px 7px;
  font-size: var(--text-sm);
  pointer-events: none;
  animation: copy-chip-feedback var(--duration-fast) var(--easing-standard);
  white-space: nowrap;
}

.copy-chip__feedback--failure {
  border-color: var(--color-feedback-danger-border);
  color: var(--color-danger);
}

@keyframes copy-chip-feedback {
  from {
    opacity: 0;
    transform: translateY(-3px);
  }
}

@media (max-width: 860px) {
  .copy-chip {
    min-height: var(--touch-target);
  }
}

@media (prefers-reduced-motion: reduce) {
  .copy-chip__feedback {
    animation: none;
  }
}
</style>
