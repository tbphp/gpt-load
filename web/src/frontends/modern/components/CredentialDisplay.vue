<script setup lang="ts">
import { Check, Eye, EyeOff, Pencil, X } from '@lucide/vue'
import { computed, nextTick, onDeactivated, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { credentialDisplayText, maskSubscriptionAccount } from '@shared/credential-display'
import { AppCopyValue, AppIconButton, AppOverflowText, AppTextField, AppTooltip } from './ui'

const props = defineProps<{
  name?: string
  value: string
  subscription?: boolean
  detail?: boolean
  reveal?: boolean
  copy?: boolean
  copyLabel?: string
  resolveValue?: () => string | Promise<string>
  saveName?: (name: string) => Promise<void>
  editButton?: boolean
  disabled?: boolean
}>()
const emit = defineEmits<{ dirty: [value: boolean]; pending: [value: boolean] }>()
const { t } = useI18n()
const revealed = ref(false)
const editing = ref(false)
const draft = ref('')
const pending = ref(false)
const error = ref('')
const input = ref<InstanceType<typeof AppTextField>>()
const dirty = computed(() => editing.value && draft.value.trim() !== (props.name ?? ''))
watch(dirty, (value) => emit('dirty', value))
watch(pending, (value) => emit('pending', value))
async function edit(): Promise<void> {
  if (!props.saveName || props.disabled || pending.value || editing.value) return
  draft.value = props.name ?? ''
  error.value = ''
  editing.value = true
  await nextTick()
  input.value?.focus()
  input.value?.select()
}
function doubleClick(event: MouseEvent): void {
  if (event.target instanceof Element && event.target.closest('button, input')) return
  void edit()
}
function cancel(): void {
  if (pending.value) return
  editing.value = false
  error.value = ''
}
async function save(): Promise<void> {
  if (!props.saveName || props.disabled || pending.value) return
  if (!dirty.value) {
    cancel()
    return
  }
  pending.value = true
  error.value = ''
  try {
    await props.saveName(draft.value.trim())
    editing.value = false
  } catch {
    error.value = t('credentialCards.nameSaveFailed')
  } finally {
    pending.value = false
  }
}
function enter(event: KeyboardEvent): void {
  if (event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  void save()
}
onScopeDispose(() => {
  emit('dirty', false)
  emit('pending', false)
})
watch(
  () => [props.value, props.name, props.reveal],
  () => {
    revealed.value = false
  },
)
onDeactivated(() => {
  revealed.value = false
})
const accountValue = computed(() =>
  props.subscription && !revealed.value ? maskSubscriptionAccount(props.value) : props.value,
)
const display = computed(() =>
  props.detail
    ? accountValue.value
    : credentialDisplayText(
        props.name,
        props.value,
        props.subscription ? 'subscription' : 'api_key',
      ),
)
</script>

<template>
  <span
    class="modern-credential-display"
    :class="{ 'has-name': detail && name, 'is-editing': editing }"
    @dblclick="doubleClick"
  >
    <span v-if="editing" class="modern-credential-name-editor" @keydown.esc.stop.prevent="cancel">
      <AppTooltip :label="error || undefined">
        <AppTextField
          ref="input"
          v-model="draft"
          class="modern-credential-name-input"
          :label="t('credentialCards.name')"
          label-hidden
          :placeholder="t('credentialCards.namePlaceholder')"
          maxlength="255"
          size="xs"
          :disabled="pending || disabled"
          :invalid="Boolean(error)"
          @keydown.enter.stop="enter"
        >
          <template #suffix>
            <span class="modern-credential-name-actions">
              <AppIconButton
                :icon="Check"
                :label="t('ui.save')"
                size="xxs"
                variant="ghost"
                :loading="pending"
                :disabled="disabled || pending"
                @click="save"
              />
              <AppIconButton
                :icon="X"
                :label="t('ui.cancel')"
                size="xxs"
                variant="ghost"
                :disabled="pending"
                @click="cancel"
              />
            </span>
          </template>
        </AppTextField>
      </AppTooltip>
    </span>
    <template v-else>
      <AppOverflowText v-if="detail && name" class="modern-credential-display-name" :text="name" />
      <span class="modern-credential-display-value">
        <AppCopyValue
          v-if="copy"
          :value="value"
          :display="display"
          :label="copyLabel"
          :resolve-value="resolveValue"
        />
        <AppOverflowText v-else :text="display" />
        <AppIconButton
          v-if="subscription && detail && reveal && value"
          class="modern-credential-display-reveal"
          :icon="revealed ? EyeOff : Eye"
          :label="t(revealed ? 'credentialCards.hideAccount' : 'credentialCards.showAccount')"
          :aria-pressed="revealed"
          size="xs"
          variant="text"
          @click.stop="revealed = !revealed"
        />
      </span>
      <AppIconButton
        v-if="saveName && editButton"
        class="modern-credential-display-edit"
        :icon="Pencil"
        :label="t('ui.edit')"
        variant="text"
        size="xxs"
        :disabled="disabled"
        @click="edit"
      />
    </template>
  </span>
</template>

<style scoped>
.modern-credential-display {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  max-width: 100%;
  vertical-align: middle;
}
.modern-credential-display-name {
  flex: 0 1 auto;
  min-width: 0;
  font-family: var(--modern-font-sans);
  font-weight: var(--modern-weight-semibold);
  color: var(--modern-text);
}
.modern-credential-display-value {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-0-5);
}
.modern-credential-display.has-name .modern-credential-display-value {
  color: var(--modern-muted);
  font-weight: var(--modern-weight-regular);
  font-size: var(--modern-font-size-small);
}
.modern-credential-display-edit {
  flex: none;
}
.modern-credential-display-reveal {
  transform: translateY(1px);
}
.modern-credential-display.is-editing {
  width: 100%;
}
.modern-credential-name-editor {
  display: flex;
  align-items: center;
  min-width: 0;
  width: 100%;
  gap: var(--modern-space-0-5);
}
.modern-credential-name-input {
  flex: 1;
  min-width: 0;
}
.modern-credential-name-actions {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: var(--modern-space-0-5);
}
</style>
