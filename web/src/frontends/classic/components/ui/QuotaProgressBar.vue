<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    value?: number
    tone?: 'success' | 'warning' | 'danger'
    label: string
    valueText: string
    compact?: boolean
  }>(),
  { value: undefined, tone: 'success', compact: false },
)

const normalizedValue = computed(() => {
  if (props.value === undefined || !Number.isFinite(props.value)) return undefined
  return Math.max(0, Math.min(100, props.value))
})
</script>

<template>
  <span
    v-if="normalizedValue !== undefined"
    class="quota-progress"
    :class="[`quota-progress--${tone}`, { 'quota-progress--compact': compact }]"
    role="progressbar"
    :aria-label="label"
    :aria-valuenow="normalizedValue"
    :aria-valuetext="valueText"
    aria-valuemin="0"
    aria-valuemax="100"
  >
    <span
      class="quota-progress__fill"
      :class="`quota-progress__fill--${tone}`"
      :style="{ width: `${normalizedValue}%` }"
    />
  </span>
  <span
    v-else
    class="quota-progress quota-progress--unknown"
    :class="{ 'quota-progress--compact': compact }"
    role="img"
    :aria-label="`${label}: ${valueText}`"
  />
</template>

<style scoped>
.quota-progress {
  position: relative;
  display: block;
  width: 100%;
  min-width: 0;
  height: 8px;
  overflow: hidden;
  border-radius: 999px;
  background: light-dark(#e9edf1, #29313a);
}
.quota-progress--compact {
  height: 6px;
}
.quota-progress--success {
  background: light-dark(#dff6e6, #1b3c29);
}
.quota-progress--warning {
  background: light-dark(#fff4cc, #3b310f);
}
.quota-progress--danger {
  background: light-dark(#ffe5e8, #421d25);
}
.quota-progress__fill {
  position: absolute;
  inset: 0 auto 0 0;
  border-radius: inherit;
  background: #42be65;
}
.quota-progress__fill--warning {
  background: #f1c21b;
}
.quota-progress__fill--danger {
  background: #fa4d56;
}
.quota-progress--unknown {
  background: repeating-linear-gradient(
    135deg,
    var(--color-border-subtle),
    var(--color-border-subtle) 6px,
    var(--color-surface-sunken) 6px,
    var(--color-surface-sunken) 12px
  );
}
</style>
