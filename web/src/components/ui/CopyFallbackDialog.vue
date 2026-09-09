<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { copyText } from '@/lib/clipboard'
import AppButton from './AppButton.vue'
import AppDialog from './AppDialog.vue'
import InlineFeedback from './InlineFeedback.vue'

const props = defineProps<{ value?: string }>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const textarea = ref<HTMLTextAreaElement>()
const pending = ref(false)
const state = ref<'idle' | 'success' | 'failure'>('idle')
let attempt = 0

function reset(): void {
  attempt += 1
  pending.value = false
  state.value = 'idle'
}

function close(): void {
  reset()
  emit('close')
}

async function copyValue(): Promise<void> {
  if (props.value === undefined || pending.value) return
  const currentAttempt = ++attempt
  pending.value = true
  state.value = 'idle'
  try {
    const copied = await copyText(props.value, textarea.value, () => currentAttempt === attempt)
    if (currentAttempt === attempt) state.value = copied ? 'success' : 'failure'
  } catch {
    if (currentAttempt === attempt) state.value = 'failure'
  } finally {
    if (currentAttempt === attempt) pending.value = false
  }
}

watch(() => props.value, reset, { flush: 'sync' })
onBeforeUnmount(reset)
</script>

<template>
  <AppDialog
    :open="value !== undefined"
    :title="t('common.copyFallback.title')"
    :description="t('common.copyFallback.description')"
    :close-label="t('common.close')"
    @update:open="!$event && close()"
  >
    <template #body>
      <div class="copy-fallback">
        <label class="copy-fallback__field">
          <span>{{ t('common.copyFallback.valueLabel') }}</span>
          <textarea
            ref="textarea"
            class="copy-fallback__value"
            :value="value"
            rows="4"
            readonly
            spellcheck="false"
          />
        </label>
        <InlineFeedback v-if="state === 'success'" tone="success">
          {{ t('common.copied') }}
        </InlineFeedback>
        <InlineFeedback v-else-if="state === 'failure'" tone="warning">
          {{ t('common.copyFallback.failed') }}
        </InlineFeedback>
      </div>
    </template>
    <template #footer>
      <AppButton variant="secondary" @click="close">{{ t('common.close') }}</AppButton>
      <AppButton :busy="pending" @click="copyValue">{{ t('common.copy') }}</AppButton>
    </template>
  </AppDialog>
</template>

<style scoped>
.copy-fallback {
  display: grid;
  gap: var(--space-3);
}

.copy-fallback__field {
  display: grid;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.copy-fallback__value {
  width: 100%;
  max-height: 280px;
  resize: vertical;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-3);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  line-height: var(--line-normal);
}
</style>
