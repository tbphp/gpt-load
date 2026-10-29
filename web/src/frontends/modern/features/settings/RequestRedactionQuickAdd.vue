<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  buildRedactionRule,
  type RedactionQuickKind,
  type RedactionRule,
} from '@shared/request-redaction'
import { AppButton, AppSelect, AppTextField } from '@modern/components/ui'

const props = defineProps<{ disabled?: boolean }>()
const emit = defineEmits<{ add: [rule: RedactionRule] }>()
const { t } = useI18n()
const id = useId()
const open = ref(false)
const kind = ref<RedactionQuickKind>('keyword')
const value = ref('')
const rule = computed(() => buildRedactionRule(kind.value, value.value))
const options = computed(() => [
  { value: 'keyword', label: t('requestRedaction.quick.keyword') },
  { value: 'prefix', label: t('requestRedaction.quick.prefix') },
])
function toggle() {
  open.value = !open.value
  if (!open.value) value.value = ''
}
function add() {
  if (props.disabled || !rule.value) return
  emit('add', rule.value)
  value.value = ''
  open.value = false
}
function onEnter(event: KeyboardEvent) {
  if (event.isComposing) return
  event.preventDefault()
  add()
}
</script>

<template>
  <div class="modern-redaction-quick">
    <div>
      <AppButton
        variant="outline"
        size="xxs"
        :disabled="disabled && !open"
        :aria-expanded="open"
        :aria-controls="id"
        @click="toggle"
        >{{ t('requestRedaction.quick.title') }}</AppButton
      >
    </div>
    <div v-if="open" :id="id" class="modern-redaction-quick-fields">
      <AppSelect
        :model-value="kind"
        :options="options"
        :label="t('requestRedaction.quick.kind')"
        size="sm"
        :disabled="disabled"
        @update:model-value="kind = $event === 'prefix' ? 'prefix' : 'keyword'"
      />
      <AppTextField
        v-model="value"
        :label="t('requestRedaction.quick.' + kind)"
        :description="t('requestRedaction.quick.' + kind + 'Help')"
        size="sm"
        :disabled="disabled"
        :spellcheck="false"
        autocomplete="off"
        :error="value.trim() && !rule ? t('requestRedaction.length') : undefined"
        @keydown.enter="onEnter"
      />
      <div v-if="rule" class="modern-redaction-quick-pattern">
        <span>{{ t('requestRedaction.pattern') }}</span
        ><code>{{ rule.pattern }}</code>
      </div>
      <div>
        <AppButton size="sm" :disabled="disabled || !rule" @click="add">{{
          t('requestRedaction.add')
        }}</AppButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modern-redaction-quick,
.modern-redaction-quick-fields,
.modern-redaction-quick-pattern {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-2);
}
.modern-redaction-quick-pattern {
  font-size: var(--modern-font-size-secondary);
  color: var(--modern-muted);
}
.modern-redaction-quick-pattern code {
  color: var(--modern-text);
  font-family: var(--modern-font-mono);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
