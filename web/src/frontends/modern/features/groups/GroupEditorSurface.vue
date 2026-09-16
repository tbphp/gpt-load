<script setup lang="ts">
import { DialogRoot } from 'reka-ui'
import { useI18n } from 'vue-i18n'
import { AppDialogContent, AppDialogHeader } from '@modern/components/ui'

withDefaults(
  defineProps<{
    title: string
    description: string
    pending?: boolean
    size?: 'default' | 'sheet'
    preventAutoFocus?: boolean
    hideDescription?: boolean
  }>(),
  { size: 'default' },
)
defineEmits<{ close: [] }>()
const { t } = useI18n()
</script>

<template>
  <DialogRoot
    :open="true"
    @update:open="
      (open) => {
        if (!open && !pending) $emit('close')
      }
    "
  >
    <AppDialogContent
      placement="editor"
      :size="size"
      :title="title"
      :description="description"
      @open-auto-focus="
        (event) => {
          if (preventAutoFocus) event.preventDefault()
        }
      "
    >
      <AppDialogHeader
        :title="title"
        :description="hideDescription ? undefined : description"
        :close-label="t('shell.close')"
        :close-disabled="pending"
        @close="$emit('close')"
      />
      <slot />
    </AppDialogContent>
  </DialogRoot>
</template>
