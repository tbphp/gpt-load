<script setup lang="ts">
import { CircleAlert } from '@lucide/vue'
import { computed } from 'vue'

import AppTooltip from './AppTooltip.vue'

const props = defineProps<{
  id: string
  error?: string
}>()

const invalid = computed(() => Boolean(props.error))
const errorId = computed(() => (invalid.value ? `${props.id}-error` : undefined))
</script>

<template>
  <span class="compact-field-error" :data-invalid="invalid || undefined">
    <slot :invalid="invalid" :described-by="errorId" :error-id="errorId" />

    <AppTooltip v-if="error && errorId" :content="error" align="end">
      <button class="compact-field-error__indicator" type="button" :aria-label="error">
        <CircleAlert :size="15" aria-hidden="true" />
      </button>
    </AppTooltip>

    <span v-if="error && errorId" :id="errorId" class="sr-only">{{ error }}</span>
  </span>
</template>

<style scoped>
.compact-field-error {
  --compact-field-error-indicator-size: 28px;
  --compact-field-error-indicator-right: 3px;
  --compact-field-error-input-gap: 7px;
  position: relative;
  display: block;
  width: 100%;
  min-width: 0;
}

.compact-field-error :deep(input) {
  padding-inline-end: calc(
    var(--compact-field-error-indicator-size) + var(--compact-field-error-indicator-right) +
      var(--compact-field-error-input-gap)
  );
}

.compact-field-error[data-invalid='true'] :deep([data-input-shell]) {
  border-color: var(--color-danger);
}

.compact-field-error[data-invalid='true'] :deep([data-input-shell]:focus-within) {
  border-color: var(--color-danger);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-danger) 15%, transparent);
}

.compact-field-error[data-invalid='true'] :deep(code) {
  color: var(--color-danger);
}

.compact-field-error__indicator {
  position: absolute;
  top: 50%;
  right: var(--compact-field-error-indicator-right);
  display: inline-flex;
  width: var(--compact-field-error-indicator-size);
  height: var(--compact-field-error-indicator-size);
  align-items: center;
  justify-content: center;
  transform: translateY(-50%);
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-danger);
  padding: 0;
  cursor: help;
  transition:
    background-color var(--duration-fast) var(--easing-standard),
    box-shadow var(--duration-fast) var(--easing-standard);
}

.compact-field-error__indicator:hover {
  background: var(--color-danger-bg);
}

.compact-field-error__indicator:focus-visible {
  background: var(--color-danger-bg);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-danger) 24%, transparent);
}

@media (max-width: 860px) {
  .compact-field-error {
    --compact-field-error-indicator-size: var(--touch-target);
    --compact-field-error-indicator-right: 0px;
    --compact-field-error-input-gap: 4px;
  }

  .compact-field-error :deep(input) {
    padding-inline-end: calc(
      var(--compact-field-error-indicator-size) + var(--compact-field-error-indicator-right) +
        var(--compact-field-error-input-gap)
    );
  }
}
</style>
