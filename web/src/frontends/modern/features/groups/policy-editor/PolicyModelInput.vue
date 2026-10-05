<script setup lang="ts">
import { computed, ref } from 'vue'
import { AppMultiSelect, type ControlSize } from '@modern/components/ui'
import { usePolicyDraftStatus } from './use-policy-draft'
import { usePolicyMessages } from './use-policy-messages'

const props = withDefaults(
  defineProps<{
    modelValue?: readonly string[]
    disabled?: boolean
    size?: ControlSize
  }>(),
  {
    modelValue: () => [],
    disabled: false,
    size: 'xs',
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
}>()

const { t } = usePolicyMessages()
const text = ref('')
const over = computed(
  () =>
    [...text.value.trim()].length > 255 ||
    props.modelValue.length > 100 ||
    (text.value.trim().length > 0 &&
      props.modelValue.length >= 100 &&
      !props.modelValue.includes(text.value.trim())),
)

usePolicyDraftStatus('models', () => ({
  active: !props.disabled,
  valid: !over.value && (props.modelValue.length > 0 || text.value.trim().length > 0),
  pending: text.value.trim().length > 0,
}))
</script>

<template>
  <AppMultiSelect
    :model-value="[...modelValue]"
    :options="[]"
    :label="t('policyEditor.action.models')"
    label-hidden
    allow-custom
    commit-on-blur
    :delimiters="[',']"
    remove-on-backspace
    :max-custom-length="255"
    :max-values="100"
    :disabled="disabled"
    :size="size"
    :error="over || (modelValue.length === 0 && text.trim().length === 0) ? t('policyEditor.action.modelsInvalid') : undefined"
    @draft-change="text = $event"
    @update:model-value="emit('update:modelValue', $event)"
  />
</template>
