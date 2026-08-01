<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    size?: number | string
    label?: string
  }>(),
  {
    size: 24,
    label: undefined,
  },
)

const dimension = computed(() => (typeof props.size === 'number' ? String(props.size) : props.size))
</script>

<template>
  <svg
    class="brand-mark"
    viewBox="0 0 24 24"
    :width="dimension"
    :height="dimension"
    fill="currentColor"
    :role="label ? 'img' : undefined"
    :aria-hidden="label ? undefined : 'true'"
    :aria-label="label"
  >
    <g transform="rotate(45 12 12)">
      <path d="M4 6.6H8V16H14.4V20H4Z" />
      <path d="M20 17.4H16V8H9.6V4H20Z" />
    </g>
  </svg>
</template>

<style scoped>
.brand-mark {
  display: block;
  flex: none;
  color: var(--color-action);
}
</style>
