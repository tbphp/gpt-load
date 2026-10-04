<script setup lang="ts">
import { Plus, Search } from '@lucide/vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppCheckbox, AppOverflowText, AppTextField } from '@modern/components/ui'
import { overlaySideOffset } from '@modern/components/ui/overlay'
import { matchesSearchOption } from '@modern/components/ui/search-options'

const props = defineProps<{ names: string[]; disabled?: boolean }>()
const emit = defineEmits<{ add: [names: string[]] }>()
const { t, n } = useI18n()
const open = ref(false)
const search = ref('')
const selected = ref<string[]>([])
const input = ref<InstanceType<typeof AppTextField>>()
const results = computed(() =>
  props.names.filter((name) => matchesSearchOption({ value: name, label: name }, search.value)),
)
const allResultsSelected = computed(() =>
  results.value.every((name) => selected.value.includes(name)),
)
function selectResults(): void {
  selected.value = [...new Set([...selected.value, ...results.value])]
}
function toggle(name: string, checked: boolean): void {
  selected.value = checked
    ? [...selected.value, name]
    : selected.value.filter((value) => value !== name)
}
function add(): void {
  emit(
    'add',
    selected.value.filter((name) => props.names.includes(name)),
  )
  open.value = false
}
function navigate(event: KeyboardEvent): void {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
  const panel = event.currentTarget as HTMLElement
  const controls = Array.from(panel.querySelectorAll<HTMLInputElement>('input'))
  const index = controls.indexOf(event.target as HTMLInputElement)
  if (index < 0) return
  event.preventDefault()
  controls[
    Math.max(0, Math.min(controls.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)))
  ]?.focus()
}
watch(open, () => {
  search.value = ''
  selected.value = []
})
watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) open.value = false
  },
)
</script>

<template>
  <PopoverRoot v-model:open="open">
    <PopoverTrigger as-child
      ><AppButton :icon="Plus" size="sm" :disabled="disabled || !names.length">{{
        t('modelManager.clientCatalog.addModels')
      }}</AppButton></PopoverTrigger
    >
    <PopoverPortal>
      <PopoverContent
        class="modern-client-catalog-picker"
        align="end"
        :side-offset="overlaySideOffset"
        :collision-padding="16"
        :aria-label="t('modelManager.clientCatalog.addLabel')"
        @open-auto-focus.prevent="input?.focus()"
        @keydown="navigate"
      >
        <AppTextField
          ref="input"
          v-model="search"
          :icon="Search"
          :label="t('modelManager.clientCatalog.addLabel')"
          :placeholder="t('modelManager.clientCatalog.search')"
          label-hidden
          size="sm"
          type="search"
          @keydown.enter.prevent
        />
        <p class="modern-client-catalog-picker-results">
          {{ t('modelManager.clientCatalog.resultCount', { count: n(results.length) }) }}
        </p>
        <div class="modern-client-catalog-picker-options">
          <AppCheckbox
            v-for="name in results"
            :key="name"
            :label="name"
            :model-value="selected.includes(name)"
            class="modern-client-catalog-picker-option"
            :class="{ 'is-selected': selected.includes(name) }"
            @update:model-value="toggle(name, $event)"
            ><AppOverflowText :text="name"
          /></AppCheckbox>
          <p v-if="!results.length" class="modern-client-catalog-picker-empty">
            {{ t('ui.select.empty') }}
          </p>
        </div>
        <footer>
          <AppButton
            variant="default"
            size="sm"
            :disabled="allResultsSelected"
            @click="selectResults"
          >
            {{ t('modelManager.clientCatalog.selectResults') }}
          </AppButton>
          <AppButton variant="primary" size="sm" :disabled="!selected.length" @click="add">{{
            t('modelManager.clientCatalog.add', { count: n(selected.length) })
          }}</AppButton>
        </footer>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<!-- Portal 根节点不会继承本组件的 scoped 标记；浮层样式使用独占类名。 -->
<style>
.modern-client-catalog-picker {
  z-index: var(--modern-layer-menu);
  display: flex;
  flex-direction: column;
  width: min(400px, calc(100vw - var(--modern-space-8)));
  max-height: min(440px, var(--reka-popper-available-height, 80dvh));
  padding: var(--modern-space-3);
  gap: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  box-shadow: var(--modern-shadow-menu);
}
.modern-client-catalog-picker-options {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  overflow-y: auto;
  min-height: 0;
  overscroll-behavior: contain;
}
.modern-client-catalog-picker-results {
  flex: none;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-client-catalog-picker .modern-client-catalog-picker-option {
  display: flex;
  flex: none;
  width: 100%;
  min-width: 0;
  padding: var(--modern-space-1) var(--modern-space-2);
  border-radius: var(--modern-radius-small);
}
.modern-client-catalog-picker .modern-client-catalog-picker-option:hover {
  background: var(--modern-control-hover);
}
.modern-client-catalog-picker .modern-client-catalog-picker-option.is-selected {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
}
.modern-client-catalog-picker-empty {
  padding: var(--modern-space-4);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-client-catalog-picker footer {
  display: flex;
  flex: none;
  justify-content: space-between;
  align-items: center;
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
</style>
