<script setup lang="ts">
defineOptions({ inheritAttrs: false })
defineProps<{ label: string; disabled?: boolean }>()
const model = defineModel<boolean>({ required: true })
</script>

<template>
  <label class="modern-checkbox" :class="{ 'is-disabled': disabled }">
    <input v-model="model" v-bind="$attrs" type="checkbox" :disabled="disabled" />
    <span>{{ label }}</span>
  </label>
</template>

<style scoped>
.modern-checkbox {
  display: inline-flex;
  width: fit-content;
  min-height: var(--modern-control-sm);
  align-items: center;
  gap: var(--modern-space-2);
  font-size: var(--modern-text-secondary);
  cursor: pointer;
}
.modern-checkbox input {
  flex: none;
  width: var(--modern-space-4);
  height: var(--modern-space-4);
  margin: 0;
  accent-color: var(--modern-accent);
  cursor: inherit;
}
.modern-checkbox.is-disabled {
  opacity: var(--modern-opacity-disabled);
  cursor: not-allowed;
}
@media (max-width: 760px) {
  .modern-checkbox {
    min-height: var(--modern-touch-target);
  }
}
</style>
