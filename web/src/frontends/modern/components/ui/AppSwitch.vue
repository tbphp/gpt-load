<script setup lang="ts">
defineProps<{ modelValue: boolean; label: string; disabled?: boolean; loading?: boolean }>()
defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <button
    class="modern-switch"
    type="button"
    role="switch"
    :aria-label="label"
    :aria-checked="modelValue"
    :aria-busy="loading || undefined"
    :disabled="disabled || loading"
    @click="$emit('update:modelValue', !modelValue)"
  >
    <span class="modern-switch-track" :class="{ 'is-on': modelValue }" aria-hidden="true"
      ><span
    /></span>
  </button>
</template>

<style scoped>
.modern-switch {
  display: inline-flex;
  min-height: var(--modern-control-md);
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  border: 0;
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-1);
  background: transparent;
}
.modern-switch-track {
  display: block;
  position: relative;
  width: var(--modern-switch-width);
  height: var(--modern-switch-height);
  border-radius: var(--modern-switch-height);
  background: var(--modern-muted);
  transition: background-color var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-switch-track > span {
  position: absolute;
  top: var(--modern-space-0-5);
  left: var(--modern-space-0-5);
  width: var(--modern-switch-thumb);
  height: var(--modern-switch-thumb);
  border-radius: var(--modern-radius-round);
  background: var(--modern-surface);
  transition: transform var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-switch-track.is-on {
  background: var(--modern-action);
}
.modern-switch-track.is-on > span {
  background: var(--modern-on-action);
  transform: translateX(calc(var(--modern-switch-width) - var(--modern-switch-height)));
}
@media (max-width: 760px) {
  .modern-switch {
    min-width: var(--modern-touch-target);
    min-height: var(--modern-touch-target);
  }
}
</style>
