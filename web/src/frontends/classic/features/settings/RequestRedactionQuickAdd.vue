<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  buildRedactionRule,
  type RedactionQuickKind,
  type RedactionRule,
} from '@shared/request-redaction'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'

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
  <div class="redaction-quick">
    <div>
      <AppButton
        variant="secondary"
        size="compact"
        :disabled="disabled && !open"
        :aria-expanded="open"
        :aria-controls="id"
        @click="toggle"
        >{{ t('requestRedaction.quick.title') }}</AppButton
      >
    </div>
    <div v-if="open" :id="id" class="redaction-quick-fields">
      <FormField :id="id + '-kind'" :label="t('requestRedaction.quick.kind')" size="compact">
        <AppSelect
          :id="id + '-kind'"
          :model-value="kind"
          :options="options"
          :label="t('requestRedaction.quick.kind')"
          size="sm"
          :disabled="disabled"
          @update:model-value="kind = $event === 'prefix' ? 'prefix' : 'keyword'"
        />
      </FormField>
      <FormField
        :id="id + '-value'"
        :label="t('requestRedaction.quick.' + kind)"
        size="compact"
        :description="t('requestRedaction.quick.' + kind + 'Help')"
        :error="value.trim() && !rule ? t('requestRedaction.length') : undefined"
      >
        <template #default="{ describedBy }">
          <AppTextInput
            :id="id + '-value'"
            v-model="value"
            :aria-describedby="describedBy"
            :label="t('requestRedaction.quick.' + kind)"
            size="sm"
            :disabled="disabled"
            :invalid="Boolean(value.trim() && !rule)"
            :spellcheck="false"
            autocomplete="off"
            @keydown.enter="onEnter"
          />
        </template>
      </FormField>
      <div v-if="rule" class="redaction-quick-pattern">
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
.redaction-quick,
.redaction-quick-fields,
.redaction-quick-pattern {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}
.redaction-quick-pattern {
  font-size: var(--text-sm);
  color: var(--color-text-muted);
}
.redaction-quick-pattern code {
  color: var(--color-text);
  font-family: var(--font-mono);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
