<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '@/components/ui/AppButton.vue'

const props = defineProps<{
  value: string
  disabled: boolean
  save: (name: string) => Promise<void>
}>()
const { t } = useI18n()
const draft = ref(props.value)
const pending = ref(false)
const error = ref(false)
watch(
  () => props.value,
  (value, previous) => {
    if (draft.value === previous || pending.value) draft.value = value
  },
)
async function submit(): Promise<void> {
  if (pending.value || props.disabled || draft.value.trim() === props.value) return
  pending.value = true
  error.value = false
  try {
    await props.save(draft.value.trim())
    draft.value = props.value
  } catch {
    error.value = true
  } finally {
    pending.value = false
  }
}
</script>
<template>
  <form class="credential-name-editor setting-panel" @submit.prevent="submit">
    <label>
      <span class="setting-panel__title">{{ t('group.credentials.name') }}</span>
      <input
        v-model="draft"
        type="text"
        maxlength="255"
        :placeholder="t('group.credentials.namePlaceholder')"
        :disabled="disabled || pending"
      />
    </label>
    <AppButton
      type="submit"
      size="compact"
      :disabled="disabled || pending || draft.trim() === value"
      >{{ t('group.credentials.weightEditor.save') }}</AppButton
    >
    <p v-if="error" role="alert">{{ t('group.credentials.nameSaveFailed') }}</p>
  </form>
</template>
<style scoped>
.credential-name-editor {
  display: flex;
  align-items: end;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.credential-name-editor label {
  display: grid;
  flex: 1;
  min-width: 0;
  gap: var(--space-2);
}
.credential-name-editor input {
  width: 100%;
  min-width: 0;
}
.credential-name-editor p {
  flex-basis: 100%;
  color: var(--color-danger);
  margin: 0;
}
</style>
