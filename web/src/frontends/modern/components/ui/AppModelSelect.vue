<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppSearchSelect from './AppSearchSelect.vue'
import { isModelPattern } from './model-match'
import type { ControlSize, FieldProps } from './types'

defineOptions({ inheritAttrs: false })
const props = defineProps<
  FieldProps & { models: readonly string[]; size?: ControlSize; fuzzy?: boolean }
>()
const model = defineModel<string>({ required: true })
const select = ref<InstanceType<typeof AppSearchSelect>>()
const { t } = useI18n()
const options = computed(() => [
  { value: '', label: t('ui.select.allModels') },
  ...[...new Set(props.models)].sort().map((value) => ({ value, label: value })),
])
defineExpose({ focus: () => select.value?.focus() })
</script>

<template>
  <AppSearchSelect
    v-bind="$attrs"
    :id="id"
    ref="select"
    v-model="model"
    :label="label"
    :label-hidden="labelHidden"
    :description="description"
    :described-by="describedBy"
    :invalid="invalid"
    :error="error || (!fuzzy && !isModelPattern(model) ? t('ui.select.modelPattern') : undefined)"
    :disabled="disabled"
    :size="size"
    :options="options"
    :placeholder="t('ui.select.allModels')"
    allow-custom
  />
</template>
