<script setup lang="ts">
import StatusBadge from '@/components/ui/StatusBadge.vue'

defineProps<{
  settingKey: string
  label: string
  description: string
  effectiveValue: number
  owned: boolean
  modelValue: number
  error?: string
  min?: number
  max?: number
  disabled?: boolean
}>()
const emit = defineEmits<{
  'update:owned': [value: boolean]
  'update:modelValue': [value: number]
}>()

function updateValue(event: Event): void {
  emit('update:modelValue', Number((event.target as HTMLInputElement).value))
}
</script>

<template>
  <div class="setting-override-field">
    <div class="setting-override-field__summary">
      <div>
        <strong>{{ label }}</strong>
        <p>{{ description }}</p>
        <small>{{ $t('settings.effectiveValue', { value: effectiveValue }) }}</small>
      </div>
      <StatusBadge :tone="owned ? 'warning' : 'neutral'">
        {{ owned ? $t('settings.override') : $t('settings.default') }}
      </StatusBadge>
    </div>
    <label class="setting-override-field__toggle">
      <input
        :id="`settings-override-${settingKey}`"
        type="checkbox"
        :checked="owned"
        :disabled="disabled"
        @change="emit('update:owned', ($event.target as HTMLInputElement).checked)"
      />
      <span>{{ $t('settings.useOverride') }}</span>
    </label>
    <label v-if="owned" class="setting-override-field__input">
      <span class="sr-only">{{ label }}</span>
      <input
        :id="`settings-value-${settingKey}`"
        type="number"
        step="1"
        :min="min"
        :max="max"
        :value="modelValue"
        :disabled="disabled"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="error ? `settings-error-${settingKey}` : undefined"
        @input="updateValue"
      />
      <small
        v-if="error"
        :id="`settings-error-${settingKey}`"
        class="setting-override-field__error"
      >
        {{ error }}
      </small>
    </label>
  </div>
</template>

<style scoped>
.setting-override-field {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) minmax(150px, auto) minmax(110px, 150px);
  align-items: center;
  gap: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-4);
}
.setting-override-field:first-child {
  border-top: 0;
  padding-top: 0;
}
.setting-override-field__summary {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.setting-override-field p,
.setting-override-field small {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}
.setting-override-field__toggle {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}
.setting-override-field__toggle input {
  width: 18px;
  height: 18px;
}
.setting-override-field__input {
  display: grid;
  gap: var(--space-1);
}
.setting-override-field__input input {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.setting-override-field__error {
  color: var(--color-danger) !important;
}
@media (max-width: 759px) {
  .setting-override-field {
    grid-template-columns: 1fr;
  }
}
</style>
