<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyOptionDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  id?: string
  modelValue?: number
  options: AccessKeyOptionDto[]
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: number | undefined] }>()
const { t } = useI18n()
const open = ref(false)
const search = ref('')
const label = (key: AccessKeyOptionDto) => `${key.name} · …${key.key_suffix}`
const selected = computed(() => props.options.find((key) => key.id === props.modelValue))
const selectedLabel = computed(() =>
  selected.value
    ? label(selected.value)
    : props.modelValue
      ? `#${props.modelValue}`
      : t('monitor.logs.filters.anyAccessKey'),
)
const matches = computed(() =>
  props.options.filter((key) =>
    `${key.name} ${key.key_suffix} ${key.id}`
      .toLocaleLowerCase()
      .includes(search.value.trim().toLocaleLowerCase()),
  ),
)
watch(open, () => {
  search.value = ''
})
function choose(id?: number): void {
  emit('update:modelValue', id)
  open.value = false
}
</script>

<template>
  <AppPopover v-model:open="open" class="access-key-select" align="start">
    <template #trigger>
      <AppButton
        v-bind="$attrs"
        :id="id"
        class="access-key-select__trigger"
        variant="secondary"
        size="compact"
        :disabled="disabled"
        :aria-label="`${t('monitor.logs.filters.accessKey')}: ${selectedLabel}`"
      >
        <span class="access-key-select__label">{{ selectedLabel }}</span>
        <ChevronDown class="access-key-select__chevron" :size="14" aria-hidden="true" />
      </AppButton>
    </template>
    <div class="access-key-select__options">
      <AppSearchInput
        v-model="search"
        :label="t('monitor.logs.filters.accessKey')"
        :placeholder="t('monitor.logs.filters.accessKey')"
        :clear-label="t('monitor.clearKeySearch')"
      />
      <button
        type="button"
        class="access-key-select__option"
        :aria-current="modelValue === undefined ? 'true' : undefined"
        @click="choose()"
      >
        {{ t('monitor.logs.filters.anyAccessKey') }}
      </button>
      <button
        v-for="key in matches"
        :key="key.id"
        type="button"
        class="access-key-select__option"
        :aria-current="key.id === modelValue ? 'true' : undefined"
        @click="choose(key.id)"
      >
        {{ label(key) }}
      </button>
    </div>
  </AppPopover>
</template>

<style scoped>
.access-key-select__trigger.app-button {
  width: 100%;
  min-width: 0;
  height: var(--control-compact);
  justify-content: space-between;
  gap: var(--space-2);
  color: var(--color-text-muted);
  padding-inline: var(--space-2);
  font: inherit;
  font-size: var(--text-sm);
  text-align: left;
}
.access-key-select__trigger.app-button:hover:not(:disabled) {
  color: var(--color-text-muted);
}
.access-key-select__trigger.app-button:disabled {
  opacity: 0.55;
}
.access-key-select__label {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.access-key-select__chevron {
  flex-shrink: 0;
}
.access-key-select__options {
  display: grid;
  gap: 4px;
}
.access-key-select__option {
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text);
  padding: 8px;
  text-align: left;
  font: inherit;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.access-key-select__option:hover,
.access-key-select__option[aria-current='true'] {
  background: var(--color-surface-sunken);
}
.access-key-select__option:focus-visible {
  outline: 2px solid var(--color-action);
  outline-offset: -2px;
}
</style>
