<script setup lang="ts">
import AppTooltip from './AppTooltip.vue'
import { Check, Copy, LoaderCircle, TriangleAlert } from '@lucide/vue'
import { DialogRoot } from 'reka-ui'
import { computed, nextTick, onScopeDispose, ref, useId, useSlots, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppDialogContent from './AppDialogContent.vue'
import AppDialogHeader from './AppDialogHeader.vue'
import AppIcon from './AppIcon.vue'
import AppOverflowText from './AppOverflowText.vue'
import { clipboardRevision, copyText } from './clipboard'
import './textarea.css'

defineOptions({ inheritAttrs: false })
const props = defineProps<{
  value: string
  display?: string
  label?: string
  wrap?: boolean
  resolveValue?: () => string | Promise<string>
}>()
const emit = defineEmits<{ copied: []; failed: [] }>()
const slots = useSlots()
const { t } = useI18n()
const id = useId()
const pending = ref(false)
const state = ref<'idle' | 'success' | 'failed'>('idle')
const fallback = ref<string>()
const textarea = ref<HTMLTextAreaElement>()
const trigger = ref<HTMLElement>()
let sequence = 0
let disposed = false
let timer: ReturnType<typeof setTimeout> | undefined
const label = computed(() =>
  state.value === 'success'
    ? t('ui.copy.success')
    : state.value === 'failed'
      ? t('ui.copy.failed')
      : (props.label ?? t('ui.copy.action')),
)
function reset(): void {
  sequence++
  clearTimeout(timer)
  pending.value = false
  state.value = 'idle'
  fallback.value = undefined
}
async function copy(manual = false): Promise<void> {
  if (pending.value) return
  if (slots.trigger && !manual && document.activeElement instanceof HTMLElement)
    trigger.value = document.activeElement
  clearTimeout(timer)
  const operation = ++sequence
  const isCurrent = () => !disposed && operation === sequence
  pending.value = true
  try {
    const value = manual
      ? (fallback.value ?? props.value)
      : props.resolveValue
        ? await props.resolveValue()
        : props.value
    if (!isCurrent()) return
    const copied = await copyText(value, manual ? textarea.value : undefined, isCurrent)
    if (!isCurrent()) return
    state.value = copied ? 'success' : 'failed'
    if (!copied) fallback.value = value
    else {
      emit('copied')
      timer = setTimeout(() => {
        state.value = 'idle'
      }, 2000)
    }
  } catch {
    if (isCurrent()) {
      state.value = 'failed'
      emit('failed')
    }
  } finally {
    if (isCurrent()) pending.value = false
  }
}
async function focusFallback(event: Event): Promise<void> {
  event.preventDefault()
  await nextTick()
  textarea.value?.focus({ preventScroll: true })
  textarea.value?.select()
}
function preserveFullCopy(event: ClipboardEvent): void {
  const input = textarea.value
  if (
    fallback.value === undefined ||
    !event.clipboardData ||
    !input ||
    input.selectionStart !== 0 ||
    input.selectionEnd !== input.value.length
  )
    return
  event.clipboardData.setData('text/plain', fallback.value)
  event.preventDefault()
}
function closeFallback(): void {
  reset()
  void nextTick(() => {
    if (!disposed) trigger.value?.focus({ preventScroll: true })
  })
}
watch(() => [props.value, props.display, props.resolveValue, clipboardRevision.value], reset, {
  flush: 'sync',
})
onScopeDispose(() => {
  disposed = true
  reset()
})
</script>

<template>
  <slot name="trigger" :copy="copy" :pending="pending">
    <span v-bind="$attrs" class="modern-copy-value" :class="{ 'is-wrapped': wrap }">
      <span v-if="wrap">{{ display ?? value }}</span>
      <AppOverflowText v-else :text="display ?? value" />
      <AppTooltip :label="label">
        <button
          ref="trigger"
          type="button"
          class="modern-copy-button"
          :class="{ 'is-copied': state === 'success' }"
          :aria-label="label"
          :disabled="pending"
          :aria-busy="pending || undefined"
          @click.stop="copy()"
        >
          <AppIcon
            :icon="
              pending
                ? LoaderCircle
                : state === 'success'
                  ? Check
                  : state === 'failed'
                    ? TriangleAlert
                    : Copy
            "
            size="inherit"
            :class="{ 'modern-spin': pending }"
          />
        </button>
      </AppTooltip>
      <span class="modern-sr-only" role="status">{{ state === 'idle' ? '' : label }}</span>
    </span>
  </slot>
  <DialogRoot
    :open="fallback !== undefined"
    @update:open="
      (open) => {
        if (!open) closeFallback()
      }
    "
  >
    <AppDialogContent
      :title="t('ui.copy.manualTitle')"
      :description="t('ui.copy.manualHelp')"
      @open-auto-focus="focusFallback"
    >
      <AppDialogHeader
        :title="t('ui.copy.manualTitle')"
        :description="t('ui.copy.manualHelp')"
        :close-label="t('ui.close')"
        @close="closeFallback"
      />
      <div class="modern-copy-fallback">
        <label :for="id" class="modern-sr-only">{{ t('ui.copy.value') }}</label>
        <textarea
          :id="id"
          ref="textarea"
          class="modern-resizable-textarea"
          :value="fallback"
          readonly
          spellcheck="false"
          @focus="textarea?.select()"
          @copy="preserveFullCopy"
        />
        <p role="status">
          {{ state === 'success' ? t('ui.copy.success') : t('ui.copy.manualHelp') }}
        </p>
        <div class="modern-copy-actions">
          <AppButton @click="closeFallback">{{ t('ui.close') }}</AppButton>
          <AppButton variant="primary" :loading="pending" @click="copy(true)">{{
            t('ui.copy.action')
          }}</AppButton>
        </div>
      </div>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-copy-value {
  display: inline-flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: var(--modern-space-0-5);
  vertical-align: middle;
}

.modern-copy-button {
  display: inline-flex;
  min-width: calc(1em + var(--modern-space-1));
  min-height: var(--modern-inline-action-target);
  flex: none;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--modern-radius-small);
  background: transparent;
  padding: var(--modern-space-0-5);
  color: var(--modern-muted);
  font: inherit;
}
.modern-copy-value.is-wrapped {
  display: block;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  line-height: var(--modern-leading-body);
}
.modern-copy-value.is-wrapped .modern-copy-button {
  margin-inline-start: var(--modern-space-0-5);
  vertical-align: middle;
}
.modern-copy-button:hover {
  background: var(--modern-control-hover);
  color: var(--modern-text);
}
.modern-copy-button.is-copied {
  color: var(--modern-success);
}
.modern-copy-fallback {
  display: grid;
  min-height: 0;
  gap: var(--modern-space-4);
  padding: var(--modern-space-5);
  overflow-y: auto;
}
.modern-copy-fallback textarea {
  width: 100%;
  min-height: 8rem;
  resize: vertical;
  border: var(--modern-line-width) solid var(--modern-control-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: var(--modern-space-3);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-body);
}
.modern-copy-fallback p {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-copy-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--modern-space-2);
}
</style>
