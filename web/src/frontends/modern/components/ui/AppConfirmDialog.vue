<script setup lang="ts">
import { CircleAlert, X } from '@lucide/vue'
import { DialogRoot } from 'reka-ui'
import { ref, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppDialogContent from './AppDialogContent.vue'
import AppIcon from './AppIcon.vue'
import AppIconButton from './AppIconButton.vue'
import AppNotice from './AppNotice.vue'
import AppOverflowText from './AppOverflowText.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    description?: string
    subject?: string
    confirmLabel: string
    cancelLabel?: string
    tone?: 'brand' | 'danger'
    icon?: Component
    pending?: boolean
    disabled?: boolean
    error?: string
  }>(),
  {
    description: undefined,
    subject: undefined,
    cancelLabel: undefined,
    tone: 'brand',
    icon: undefined,
    error: undefined,
  },
)
const emit = defineEmits<{ confirm: []; cancel: []; closeAutoFocus: [event: Event] }>()
const { t } = useI18n()
const actions = ref<HTMLElement>()
function cancel(): void {
  if (!props.pending) emit('cancel')
}
function focusInitialAction(event: Event): void {
  event.preventDefault()
  const action =
    !props.disabled && !props.pending ? '[data-confirm-action]' : '[data-cancel-action]'
  actions.value?.querySelector<HTMLButtonElement>(action)?.focus({ preventScroll: true })
}
</script>

<template>
  <DialogRoot
    :open="open"
    @update:open="
      (value) => {
        if (!value) cancel()
      }
    "
  >
    <AppDialogContent
      size="confirm"
      :title="title"
      :description="description || title"
      @open-auto-focus="focusInitialAction"
      @close-auto-focus="emit('closeAutoFocus', $event)"
      @escape-key-down="
        (event: Event) => {
          if (pending) event.preventDefault()
        }
      "
      @interact-outside="
        (event: Event) => {
          if (pending) event.preventDefault()
        }
      "
    >
      <div
        class="modern-confirm"
        :class="`modern-confirm--${tone}`"
        :aria-busy="pending || undefined"
      >
        <header class="modern-confirm-heading">
          <span class="modern-confirm-icon"><AppIcon :icon="icon || CircleAlert" size="lg" /></span>
          <h2>{{ title }}</h2>
          <AppIconButton
            :icon="X"
            :label="t('shell.close')"
            size="sm"
            :disabled="pending"
            @click="cancel"
          />
        </header>
        <div v-if="subject || description || error || $slots.default" class="modern-confirm-body">
          <div v-if="subject" class="modern-confirm-subject">
            <AppOverflowText :text="subject" />
          </div>
          <p v-if="description">{{ description }}</p>
          <slot />
          <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
        </div>
        <footer ref="actions" class="modern-confirm-actions">
          <AppButton data-cancel-action :disabled="pending" @click="cancel">{{
            cancelLabel || t('ui.cancel')
          }}</AppButton>
          <AppButton
            data-confirm-action
            :variant="tone === 'danger' ? 'danger' : 'primary'"
            :loading="pending"
            :disabled="disabled || pending"
            @click="emit('confirm')"
            >{{ confirmLabel }}</AppButton
          >
        </footer>
      </div>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-confirm {
  display: grid;
  gap: var(--modern-space-4);
  overflow-y: auto;
  padding: var(--modern-space-5);
}
.modern-confirm-heading {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
}
.modern-confirm-heading h2 {
  flex: 1;
  min-width: 0;
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  overflow-wrap: anywhere;
}
.modern-confirm-icon {
  display: grid;
  place-items: center;
  flex: none;
  width: var(--modern-control-md);
  height: var(--modern-control-md);
  border-radius: var(--modern-radius-control);
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
}
.modern-confirm--danger .modern-confirm-icon {
  background: var(--modern-danger-soft);
  color: var(--modern-danger);
}
.modern-confirm-body {
  display: grid;
  gap: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-body);
}
.modern-confirm-subject {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
}
.modern-confirm-actions {
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  padding-top: var(--modern-space-1);
}
.modern-confirm-actions :deep([data-cancel-action]:focus),
.modern-confirm-actions :deep([data-confirm-action]:focus) {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
  box-shadow: var(--modern-shadow-focus);
}
</style>
