<script setup lang="ts">
import AppButton from './AppButton.vue'
import AppDialog from './AppDialog.vue'

withDefaults(
  defineProps<{
    open: boolean
    title: string
    description: string
    closeLabel: string
    cancelLabel: string
    confirmLabel: string
    tone?: 'default' | 'danger'
    pending?: boolean
    confirmDisabled?: boolean
    dismissible?: boolean
    preventCloseAutoFocus?: boolean
  }>(),
  {
    tone: 'default',
    pending: false,
    confirmDisabled: false,
    dismissible: true,
    preventCloseAutoFocus: false,
  },
)
const emit = defineEmits<{
  'update:open': [open: boolean]
  confirm: []
}>()
</script>

<template>
  <AppDialog
    :open="open"
    :title="title"
    :description="description"
    :close-label="closeLabel"
    :dismissible="dismissible && !pending"
    :prevent-close-auto-focus="preventCloseAutoFocus"
    @update:open="emit('update:open', $event)"
  >
    <template v-if="$slots.trigger" #trigger><slot name="trigger" /></template>
    <template v-if="$slots.default" #body><slot /></template>
    <template #footer>
      <AppButton
        variant="secondary"
        size="compact"
        :disabled="pending"
        @click="emit('update:open', false)"
      >
        {{ cancelLabel }}
      </AppButton>
      <AppButton
        :variant="tone === 'danger' ? 'danger' : 'primary'"
        size="compact"
        :busy="pending"
        :disabled="confirmDisabled"
        @click="emit('confirm')"
      >
        <slot name="confirm-icon" />
        {{ confirmLabel }}
      </AppButton>
    </template>
  </AppDialog>
</template>
