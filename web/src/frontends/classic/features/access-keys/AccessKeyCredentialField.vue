<script setup lang="ts">
import { Dice5, Eye, EyeOff } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import AppTextInput from '@/components/ui/AppTextInput.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'

import { estimateAccessKeyStrength, isValidCustomAccessKey } from './access-key-strength'

const props = withDefaults(
  defineProps<{
    modelValue: string
    disabled: boolean
    error?: string
    editing?: boolean
    currentMask?: string
  }>(),
  { editing: false, currentMask: '', error: '' },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const input = ref<InstanceType<typeof AppTextInput>>()
const visible = ref(false)
const generationFailed = ref(false)
const invalid = computed(() => !isValidCustomAccessKey(props.modelValue))
const strength = computed(() => estimateAccessKeyStrength(props.modelValue))
const filledSegments = computed(() => ({ weak: 1, fair: 2, strong: 3 })[strength.value ?? 'weak'])

watch(
  () => props.modelValue,
  (value) => {
    generationFailed.value = false
    if (value === '') visible.value = false
  },
)

function focus(): void {
  input.value?.focus()
}

function generateKey(): void {
  generationFailed.value = false
  try {
    const bytes = new Uint8Array(16)
    globalThis.crypto.getRandomValues(bytes)
    const random = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
    emit('update:modelValue', `sk-gl-${random}`)
  } catch {
    generationFailed.value = true
  }
}
defineExpose({ focus })
</script>

<template>
  <div class="access-key-credential">
    <div class="access-key-credential__heading">
      <label
        id="access-key-custom-label"
        for="access-key-custom-value"
        class="access-key-credential__label"
      >
        {{ t('accessKeys.customKey.label') }}
        <small>{{ t('accessKeys.drawer.optional') }}</small>
      </label>
    </div>
    <small v-if="editing && currentMask">
      {{ t('accessKeys.customKey.current') }} <code>{{ currentMask }}</code>
    </small>
    <div
      class="access-key-credential__input-group"
      :class="{ 'access-key-credential__input-group--invalid': invalid || !!error }"
      data-input-shell
      role="group"
      aria-labelledby="access-key-custom-label"
    >
      <AppTextInput
        id="access-key-custom-value"
        ref="input"
        :model-value="modelValue"
        :label="t('accessKeys.customKey.label')"
        :type="visible ? 'text' : 'password'"
        :placeholder="
          t(editing ? 'accessKeys.customKey.editPlaceholder' : 'accessKeys.customKey.placeholder')
        "
        :disabled="disabled"
        :invalid="invalid || !!error"
        :spellcheck="false"
        autocomplete="new-password"
        autocapitalize="none"
        described-by="access-key-custom-description"
        aria-labelledby="access-key-custom-label"
        appearance="surface"
        size="compact"
        monospace
        @update:model-value="emit('update:modelValue', $event)"
      >
        <template #trailing>
          <IconButton
            variant="ghost"
            size="compact"
            :disabled="disabled"
            :label="t(visible ? 'accessKeys.customKey.hide' : 'accessKeys.customKey.show')"
            :aria-pressed="visible"
            @click="visible = !visible"
          >
            <EyeOff v-if="visible" :size="15" aria-hidden="true" />
            <Eye v-else :size="15" aria-hidden="true" />
          </IconButton>
        </template>
      </AppTextInput>
      <AppTooltip :content="t('accessKeys.customKey.generate')" :disabled="disabled">
        <IconButton
          class="access-key-credential__generate"
          size="compact"
          :disabled="disabled"
          :label="t('accessKeys.customKey.generate')"
          @click="generateKey"
        >
          <Dice5 :size="16" aria-hidden="true" />
        </IconButton>
      </AppTooltip>
    </div>
    <div
      id="access-key-custom-description"
      class="access-key-credential__description"
      aria-live="polite"
    >
      <p v-if="generationFailed" class="access-key-credential__error">
        {{ t('accessKeys.customKey.generateFailed') }}
      </p>
      <p v-if="invalid || error" class="access-key-credential__error">
        {{ error || t('accessKeys.customKey.invalid') }}
      </p>
      <template v-else-if="strength">
        <div
          class="access-key-credential__strength"
          :class="`access-key-credential__strength--${strength}`"
        >
          <span class="access-key-credential__segments" aria-hidden="true">
            <span
              v-for="segment in 3"
              :key="segment"
              :class="{ filled: segment <= filledSegments }"
            />
          </span>
          <span>{{
            t('accessKeys.customKey.strength', { level: t(`accessKeys.customKey.${strength}`) })
          }}</span>
          <small>{{ t('accessKeys.customKey.estimate') }}</small>
        </div>
        <p v-if="strength === 'weak'">{{ t('accessKeys.customKey.weakHint') }}</p>
        <p>{{ t('accessKeys.customKey.saveHint') }}</p>
      </template>
      <p v-else>
        {{ t(editing ? 'accessKeys.customKey.keepHint' : 'accessKeys.customKey.automaticHint') }}
      </p>
    </div>
  </div>
</template>

<style scoped>
.access-key-credential {
  display: grid;
  gap: 6px;
}
.access-key-credential__heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.access-key-credential__label {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}
.access-key-credential__input-group {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  min-width: 0;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.access-key-credential__input-group--invalid {
  border-color: var(--color-danger);
}
.access-key-credential__input-group :deep(.app-text-input) {
  border: 0;
  border-radius: var(--radius-control) 0 0 var(--radius-control);
}
.access-key-credential__input-group :deep(.app-text-input[data-input-shell]:focus-within) {
  outline: 0;
  box-shadow: none;
}
.access-key-credential__generate {
  height: 100%;
  align-self: stretch;
  border: 0;
  border-left: 1px solid var(--color-border-subtle);
  border-radius: 0 var(--radius-control) var(--radius-control) 0;
}
@media (max-width: 860px) {
  .access-key-credential__generate {
    width: var(--touch-target);
  }
}
.access-key-credential small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 400;
}
.access-key-credential__label small {
  margin-left: var(--space-1);
}
.access-key-credential__description {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.access-key-credential__description p {
  margin: 0;
}
.access-key-credential__description .access-key-credential__error {
  color: var(--color-danger);
}
.access-key-credential__strength {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.access-key-credential__strength--weak {
  color: var(--color-danger);
}
.access-key-credential__strength--fair {
  color: var(--color-warning);
}
.access-key-credential__strength--strong {
  color: var(--color-success);
}
.access-key-credential__strength + p {
  margin-top: 4px;
}
.access-key-credential__segments {
  display: flex;
  gap: 3px;
  width: 60px;
}
.access-key-credential__segments span {
  flex: 1;
  height: 3px;
  border-radius: var(--radius-control);
  background: var(--color-border-subtle);
}
.access-key-credential__segments .filled {
  background: currentColor;
}
</style>
