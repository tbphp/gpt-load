<script setup lang="ts">
import AppTooltip from './AppTooltip.vue'
import { LoaderCircle, Pencil, Save, X } from '@lucide/vue'
import { PopoverAnchor, PopoverContent, PopoverPortal, PopoverRoot } from 'reka-ui'
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppFieldControl from './AppFieldControl.vue'
import AppIcon from './AppIcon.vue'
import AppMenuSurface from './AppMenuSurface.vue'
import { overlaySideOffset } from './overlay'

const props = defineProps<{
  modelValue: number
  label: string
  min: number
  max: number
  pending?: boolean
  disabled?: boolean
  error?: string
}>()
const emit = defineEmits<{
  submit: [value: number]
  editing: [value: boolean]
  dirty: [value: boolean]
  clearError: []
}>()
const { t } = useI18n()
const id = useId()
const editing = ref(false)
const draft = ref(String(props.modelValue))
const attempted = ref(false)
const submitted = ref(false)
const input = ref<HTMLInputElement>()
const action = ref<HTMLButtonElement>()
const dirty = computed(() => editing.value && draft.value !== String(props.modelValue))
const invalid = computed(
  () =>
    !/^\d+$/u.test(draft.value) ||
    Number(draft.value) < props.min ||
    Number(draft.value) > props.max,
)
const error = computed(
  () =>
    props.error ||
    (attempted.value && invalid.value
      ? t('ui.number.range', { min: props.min, max: props.max })
      : ''),
)
watch(dirty, (value) => emit('dirty', value))
watch(editing, (value) => emit('editing', value))
watch(
  () => props.modelValue,
  (value) => {
    if (!editing.value) draft.value = String(value)
  },
)
watch(
  () => props.pending,
  (value) => {
    if (!value && submitted.value) {
      submitted.value = false
      if (!props.error) void cancel()
    }
  },
)
async function start(): Promise<void> {
  if (editing.value || props.disabled || props.pending) return
  editing.value = true
  draft.value = String(props.modelValue)
  attempted.value = false
  emit('clearError')
  await nextTick()
  input.value?.focus({ preventScroll: true })
  input.value?.select()
}
async function cancel(): Promise<void> {
  if (props.pending) return
  editing.value = false
  draft.value = String(props.modelValue)
  attempted.value = false
  emit('clearError')
  await nextTick()
  action.value?.focus({ preventScroll: true })
}
function submit(): void {
  if (props.pending || props.disabled) return
  if (!dirty.value) {
    void cancel()
    return
  }
  attempted.value = true
  if (invalid.value) {
    input.value?.focus()
    return
  }
  submitted.value = true
  emit('submit', Number(draft.value))
}
function enter(event: KeyboardEvent): void {
  if (event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  if (editing.value) submit()
  else void start()
}
defineExpose({ cancel })
</script>

<template>
  <div class="modern-inline-number" @keydown.esc.stop.prevent="cancel">
    <PopoverRoot :open="Boolean(error)">
      <PopoverAnchor as-child>
        <AppFieldControl size="xs" :invalid="Boolean(error)" :disabled="disabled || pending">
          <input
            ref="input"
            v-model="draft"
            :aria-label="label"
            :aria-invalid="Boolean(error) || undefined"
            :aria-describedby="error ? id : undefined"
            :readonly="!editing"
            :disabled="disabled || pending"
            inputmode="numeric"
            @click="start"
            @input="emit('clearError')"
            @keydown.enter="enter"
          />
          <AppTooltip :label="editing ? t('ui.save') : t('ui.edit')">
            <button
              ref="action"
              type="button"
              :disabled="disabled || pending"
              :aria-label="editing ? t('ui.save') : t('ui.edit')"
              @click="editing ? submit() : start()"
            >
              <AppIcon
                :icon="pending ? LoaderCircle : editing ? Save : Pencil"
                size="xs"
                :class="{ 'modern-spin': pending }"
              />
            </button>
          </AppTooltip>
        </AppFieldControl>
      </PopoverAnchor>
      <PopoverPortal>
        <AppMenuSurface>
          <PopoverContent
            align="end"
            :side-offset="overlaySideOffset"
            @open-auto-focus.prevent
            @close-auto-focus.prevent
          >
            <p :id="id" class="modern-inline-number-error" role="alert">{{ error }}</p>
          </PopoverContent>
        </AppMenuSurface>
      </PopoverPortal>
    </PopoverRoot>
    <AppTooltip :label="t('ui.cancel')" :disabled="!editing">
      <button
        type="button"
        class="modern-inline-number-cancel"
        :class="{ 'is-hidden': !editing }"
        :disabled="!editing || disabled || pending"
        :aria-label="t('ui.cancel')"
        @click="cancel"
      >
        <AppIcon :icon="X" size="xs" />
      </button>
    </AppTooltip>
  </div>
</template>

<style scoped>
.modern-inline-number {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  width: var(--modern-inline-number-width);
  flex: none;
}
.modern-inline-number > :first-child {
  flex: 1;
  min-width: 0;
}
.modern-inline-number-cancel.is-hidden {
  visibility: hidden;
}
.modern-inline-number input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  padding: 0;
  font: inherit;
  text-align: left;
  font-variant-numeric: tabular-nums;
}
.modern-inline-number button {
  display: grid;
  min-width: var(--modern-inline-action-target);
  min-height: var(--modern-inline-action-target);
  flex: none;
  place-items: center;
  border: 0;
  border-radius: var(--modern-radius-small);
  background: transparent;
  padding: 0;
  color: var(--modern-muted);
}
.modern-inline-number button:hover {
  background: var(--modern-control-hover);
  color: var(--modern-accent);
}
.modern-inline-number-error {
  max-width: 240px;
  padding: var(--modern-space-2);
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
  overflow-wrap: anywhere;
}
@media (max-width: 760px) {
  .modern-inline-number {
    width: 160px;
  }
  .modern-inline-number input {
    font-size: var(--modern-font-size-input-mobile);
  }
  .modern-inline-number button {
    min-width: var(--modern-touch-target);
    min-height: var(--modern-touch-target);
  }
}
</style>
