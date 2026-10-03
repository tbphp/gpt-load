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
  <AppField :label="label" :error="error" inline="subgrid">
    <template #label-extra>
      <AppIconButton
        v-if="custom"
        :icon="RotateCcw"
        :label="t('modelManager.profile.restoreField', { value: automatic })"
        size="xxs"
        :disabled="disabled"
        @click="$emit('reset')"
      />
    </template>
    <template #default="{ id, describedBy, invalid }"
      ><slot :id="id" :described-by="describedBy" :invalid="invalid"
    /></template>
  </AppField>
</template>
