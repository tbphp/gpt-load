<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppSegmentedControl } from '@modern/components/ui'

defineProps<{
  label: string
  description?: string
  automatic: string
  custom: boolean
  disabled?: boolean
  error?: string
}>()
const emit = defineEmits<{ 'update:custom': [value: boolean] }>()
const { t } = useI18n()
const modeOptions = computed(() => [
  { value: 'automatic', label: t('modelManager.profile.modes.automatic') },
  { value: 'custom', label: t('modelManager.profile.modes.custom') },
])

function updateMode(value: string): void {
  emit('update:custom', value === 'custom')
}
</script>

<template>
  <section class="modern-model-profile-field">
    <header class="modern-model-profile-field-heading">
      <div>
        <h3>{{ label }}</h3>
        <p v-if="description">{{ description }}</p>
      </div>
      <AppSegmentedControl
        :model-value="custom ? 'custom' : 'automatic'"
        :label="t('modelManager.profile.modeLabel', { field: label })"
        :options="modeOptions"
        appearance="field"
        size="xxs"
        :disabled="disabled"
        @update:model-value="updateMode"
      />
    </header>
    <div class="modern-model-profile-field-control"><slot :disabled="disabled || !custom" /></div>
    <p v-if="custom" class="modern-model-profile-field-automatic">
      {{ t('modelManager.profile.automaticPreview', { value: automatic }) }}
    </p>
    <p v-if="error" class="modern-model-profile-field-error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.modern-model-profile-field {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-model-profile-field + .modern-model-profile-field {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-3);
}
.modern-model-profile-field-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
.modern-model-profile-field-heading > div {
  min-width: 0;
}
.modern-model-profile-field-heading h3 {
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-model-profile-field-heading p,
.modern-model-profile-field-automatic {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  overflow-wrap: anywhere;
}
.modern-model-profile-field-heading p {
  margin-top: var(--modern-space-1);
}
.modern-model-profile-field-control {
  min-width: 0;
}
.modern-model-profile-field-error {
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
@media (max-width: 760px) {
  .modern-model-profile-field-heading {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
