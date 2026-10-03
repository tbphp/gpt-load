<script setup lang="ts">
import { RotateCcw } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { AppField, AppIconButton } from '@modern/components/ui'
defineProps<{
  label: string
  automatic: string
  custom: boolean
  disabled?: boolean
  error?: string
}>()
defineEmits<{ reset: [] }>()
const { t } = useI18n()
</script>
<template>
  <AppField :label="label" :error="error" class="modern-model-profile-field">
    <template #label-extra>
      <AppIconButton
        v-if="custom"
        :icon="RotateCcw"
        :label="t('modelManager.profile.restoreField', { value: automatic })"
        size="xxs"
        :disabled="disabled"
        @click="$emit('reset')"
      />
      <span v-else class="modern-model-profile-auto">{{
        t('modelManager.profile.automatic')
      }}</span>
    </template>
    <template #default="{ id, describedBy, invalid }"
      ><slot :id="id" :described-by="describedBy" :invalid="invalid"
    /></template>
  </AppField>
</template>
<style scoped>
.modern-model-profile-field {
  min-width: 0;
}
.modern-model-profile-auto {
  display: inline-flex;
  align-items: center;
  min-height: var(--modern-control-xxs);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
</style>
