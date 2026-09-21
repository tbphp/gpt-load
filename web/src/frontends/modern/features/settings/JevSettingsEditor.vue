<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { JevConfig, DecisionRoute } from '@modern/api/experimental'
import { AppNotice, AppSearchSelect, AppTextField } from '@modern/components/ui'

const props = defineProps<{
  modelValue: JevConfig
  routes: DecisionRoute[]
  disabled?: boolean
  error?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: JevConfig] }>()
const { t } = useI18n()
const groups = computed(() => {
  const options = props.routes.map((r) => ({ value: String(r.group_id), label: r.group_name }))
  if (
    props.modelValue.group_id &&
    !options.some((o) => o.value === String(props.modelValue.group_id))
  )
    options.push({ value: String(props.modelValue.group_id), label: t('jev.deletedGroup') })
  return [{ value: '0', label: t('jev.anyGroup') }, ...options]
})
const models = computed(() =>
  [
    ...new Set(
      props.routes
        .filter((r) => !props.modelValue.group_id || r.group_id === props.modelValue.group_id)
        .flatMap((r) => r.models),
    ),
  ].map((value) => ({ value, label: value })),
)
function update(value: Partial<JevConfig>) {
  emit('update:modelValue', { ...props.modelValue, ...value })
}
function selectGroup(value: string) {
  const group_id = Number(value)
  const available = props.routes
    .filter((r) => !group_id || r.group_id === group_id)
    .flatMap((r) => r.models)
  update({
    group_id,
    model: available.includes(props.modelValue.model)
      ? props.modelValue.model
      : (available[0] ?? ''),
  })
}
</script>

<template>
  <div class="modern-jev-settings">
    <AppNotice v-if="error" class="modern-jev-error" tone="danger">{{
      t('requestAudit.invalid')
    }}</AppNotice>
    <AppSearchSelect
      :model-value="String(modelValue.group_id)"
      :options="groups"
      :label="t('jev.group')"
      :disabled="disabled"
      @update:model-value="selectGroup"
    />
    <AppSearchSelect
      :model-value="modelValue.model"
      :options="models"
      :selected-option="{ value: modelValue.model, label: modelValue.model }"
      :label="t('jev.model')"
      :disabled="disabled"
      @update:model-value="update({ model: $event })"
    />
    <AppTextField
      :model-value="String(modelValue.timeout_seconds)"
      inputmode="numeric"
      :label="t('jev.timeout')"
      :disabled="disabled"
      @update:model-value="update({ timeout_seconds: Number($event) })"
    />
  </div>
</template>

<style scoped>
.modern-jev-settings {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(0, 0.6fr);
  gap: var(--modern-space-3);
  min-width: 0;
}
.modern-jev-error {
  grid-column: 1 / -1;
}
@container modern-settings-content (max-width: 620px) {
  .modern-jev-settings {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
