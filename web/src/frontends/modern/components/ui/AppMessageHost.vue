<script setup lang="ts">
import { X } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppIconButton from './AppIconButton.vue'
import AppNotice from './AppNotice.vue'
import type { AppMessage } from './message'
withDefaults(defineProps<{ message?: AppMessage }>(), { message: undefined })
defineEmits<{ close: [id: number] }>()
const { t } = useI18n()
const pending = ref(false)
async function act(action: NonNullable<AppMessage['action']>): Promise<void> {
  if (pending.value) return
  pending.value = true
  try {
    await action.run()
  } finally {
    pending.value = false
  }
}
</script>
<template>
  <Teleport to="body">
    <div v-if="message" class="modern-message-host">
      <AppNotice class="modern-message" :tone="message.tone" bordered>
        {{ message.text }}
        <template #actions>
          <AppButton
            v-if="message.action"
            size="sm"
            :loading="pending"
            @click="act(message.action)"
            >{{ message.action.label }}</AppButton
          >
          <AppIconButton
            :icon="X"
            :label="t('ui.close')"
            size="xs"
            @click="$emit('close', message.id)"
          />
        </template>
      </AppNotice>
    </div>
  </Teleport>
</template>
<style scoped>
.modern-message-host {
  position: fixed;
  z-index: var(--modern-layer-message);
  top: var(--modern-space-2);
  left: 50%;
  transform: translateX(-50%);
  width: max-content;
  max-width: min(480px, calc(100vw - var(--modern-space-8)));
  pointer-events: auto;
  border-radius: var(--modern-radius-control);
  box-shadow: var(--modern-shadow-control);
}
.modern-message {
  flex-wrap: nowrap;
  gap: var(--modern-space-2);
  padding: var(--modern-space-1) var(--modern-space-2) var(--modern-space-1) var(--modern-space-3);
  border-color: color-mix(in srgb, var(--modern-notice-color) 25%, transparent);
  font-size: var(--modern-font-size-small);
}
.modern-message :deep(.modern-notice-icon) {
  align-self: center;
  width: var(--modern-space-4);
  height: var(--modern-space-4);
  margin-top: 0;
}
.modern-message :deep(.modern-notice-actions) {
  gap: var(--modern-space-1);
}
</style>
