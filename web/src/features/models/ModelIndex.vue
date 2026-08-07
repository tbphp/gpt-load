<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ClientModelDto } from '@/app/resources/models'

import { presentClientModel, type ClientModelPresentation } from './model-presenter'

const props = defineProps<{
  items: ClientModelDto[]
  selected: string | null
}>()
const emit = defineEmits<{ 'update:selected': [clientModel: string] }>()
const { t } = useI18n()

const presentations = computed<ClientModelPresentation[]>(() =>
  props.items.map((model) => presentClientModel(model)),
)

/** 无论套数多少都展示，避免"恰好是 1 就不显示"造成的展示位不一致。 */
function priceCountLabel(presentation: ClientModelPresentation): string {
  return t('models.index.priceCount', { count: presentation.scopes.length })
}

function select(clientModel: string): void {
  if (clientModel !== props.selected) emit('update:selected', clientModel)
}

function moveSelection(offset: number): void {
  if (presentations.value.length === 0) return
  const index = presentations.value.findIndex((p) => p.model.client_model === props.selected)
  const base = index === -1 ? 0 : index + offset
  const bounded = Math.min(Math.max(base, 0), presentations.value.length - 1)
  const target = presentations.value[bounded]
  if (target) select(target.model.client_model)
}

function selectEdge(edge: 'first' | 'last'): void {
  const target = edge === 'first' ? presentations.value[0] : presentations.value.at(-1)
  if (target) select(target.model.client_model)
}

function handleKeydown(event: KeyboardEvent): void {
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      moveSelection(1)
      break
    case 'ArrowUp':
      event.preventDefault()
      moveSelection(-1)
      break
    case 'Home':
      event.preventDefault()
      selectEdge('first')
      break
    case 'End':
      event.preventDefault()
      selectEdge('last')
      break
  }
}
</script>

<template>
  <ul
    class="model-index"
    role="listbox"
    :aria-label="t('models.index.label')"
    @keydown="handleKeydown"
  >
    <li v-for="presentation in presentations" :key="presentation.model.client_model">
      <button
        type="button"
        class="model-index__item"
        role="option"
        :aria-selected="presentation.model.client_model === selected"
        @click="select(presentation.model.client_model)"
      >
        <span
          class="model-index__dot"
          :data-tone="presentation.status === 'configured' ? 'success' : 'warning'"
          aria-hidden="true"
        />
        <span class="model-index__name" :title="presentation.model.client_model">
          {{ presentation.model.client_model }}
        </span>
        <span class="model-index__badge">{{ priceCountLabel(presentation) }}</span>
      </button>
    </li>
  </ul>
</template>

<style scoped>
.model-index {
  display: grid;
  min-width: 0;
  /* 清单是 models-page__index 的 1fr 行；不加 start 会让 li 被均匀拉伸到右栏高度。 */
  align-content: start;
  margin: 0;
  padding: 0;
  list-style: none;
}

.model-index__item {
  display: grid;
  width: 100%;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
  border: 0;
  border-left: 2px solid transparent;
  background: none;
  cursor: pointer;
  padding: 0 var(--space-3) 0 calc(var(--space-3) - 2px);
  height: 32px;
  color: inherit;
  font: inherit;
  text-align: left;
  transition:
    background-color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard);
}

.model-index__item:hover {
  background: var(--color-interactive-hover);
}

.model-index__item[aria-selected='true'] {
  border-left-color: var(--color-action);
  background: var(--color-surface-sunken);
}

.model-index__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-text-faint);
}

.model-index__dot[data-tone='success'] {
  background: var(--color-success);
}

.model-index__dot[data-tone='warning'] {
  background: var(--color-warning);
}

.model-index__name {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-index__badge {
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 999px) {
  .model-index__item {
    height: 44px;
  }
}
</style>
