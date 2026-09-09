<script setup lang="ts">
import { Eye, EyeOff } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import AppTextInput from '@/components/ui/AppTextInput.vue'
import IconButton from '@/components/ui/IconButton.vue'

import { estimateAccessKeyStrength, isValidCustomAccessKey } from './access-key-strength'

const props = defineProps<{ modelValue: string; disabled: boolean; error?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const input = ref<InstanceType<typeof AppTextInput>>()
const visible = ref(false)
const invalid = computed(() => !isValidCustomAccessKey(props.modelValue))
const strength = computed(() => estimateAccessKeyStrength(props.modelValue))
const filledSegments = computed(() => ({ weak: 1, fair: 2, strong: 3 })[strength.value ?? 'weak'])

watch(
  () => props.modelValue,
  (value) => {
    if (value === '') visible.value = false
  },
)

function focus(): void {
  input.value?.focus()
}
defineExpose({ focus })
</script>

<template>
  <div class="access-key-credential">
    <label
      id="access-key-custom-label"
      for="access-key-custom-value"
      class="access-key-credential__label"
    >
      {{ t('accessKeys.customKey.label') }}
      <small>{{ t('accessKeys.drawer.optional') }}</small>
    </label>
    <AppTextInput
      id="access-key-custom-value"
      ref="input"
      :model-value="modelValue"
      :label="t('accessKeys.customKey.label')"
      :type="visible ? 'text' : 'password'"
      :placeholder="t('accessKeys.customKey.placeholder')"
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
    <div
      id="access-key-custom-description"
      class="access-key-credential__description"
      aria-live="polite"
    >
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
      </template>
      <p v-else>{{ t('accessKeys.customKey.automaticHint') }}</p>
    </div>
  </div>
</template>

<style scoped>
.access-key-credential {
  display: grid;
  gap: 6px;
}
.access-key-credential__label {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
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
