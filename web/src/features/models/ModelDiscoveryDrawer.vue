<script setup lang="ts">
import { Search } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'

import type { ModelDiscoveryDrawerLabels } from './model-draft'

type DiscoveryFilter = 'unadded' | 'all'

const props = withDefaults(
  defineProps<{
    open: boolean
    candidates: readonly string[]
    currentIds: readonly string[]
    loading: boolean
    error: string
    labels: ModelDiscoveryDrawerLabels
    dismissible?: boolean
  }>(),
  { dismissible: true },
)
const emit = defineEmits<{
  'update:open': [open: boolean]
  retry: []
  confirm: [candidates: string[]]
}>()

const search = ref('')
const filter = ref<DiscoveryFilter>('unadded')
const selectedCandidates = ref<string[]>([])
const normalizedCandidates = computed(() => [
  ...new Set(props.candidates.map((candidate) => candidate.trim()).filter(Boolean)),
])
const currentIds = computed(
  () => new Set(props.currentIds.map((candidate) => candidate.trim()).filter(Boolean)),
)
const selected = computed(() => new Set(selectedCandidates.value))
const visibleCandidates = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return normalizedCandidates.value.filter(
    (candidate) =>
      (filter.value === 'all' || !currentIds.value.has(candidate)) &&
      (!query || candidate.toLocaleLowerCase().includes(query)),
  )
})
const selectableVisibleCandidates = computed(() =>
  visibleCandidates.value.filter((candidate) => !currentIds.value.has(candidate)),
)
const allVisibleSelected = computed(
  () =>
    selectableVisibleCandidates.value.length > 0 &&
    selectableVisibleCandidates.value.every((candidate) => selected.value.has(candidate)),
)
const filterOptions = computed(() => [
  { value: 'unadded', label: props.labels.filterUnadded },
  { value: 'all', label: props.labels.filterAll },
])

watch(
  () => props.open,
  (open) => {
    if (!open) return
    search.value = ''
    filter.value = 'unadded'
    selectedCandidates.value = []
  },
)

watch(
  () => props.candidates,
  () => {
    const valid = new Set(normalizedCandidates.value)
    selectedCandidates.value = selectedCandidates.value.filter(
      (candidate) => valid.has(candidate) && !currentIds.value.has(candidate),
    )
  },
)

watch(
  () => props.currentIds,
  () => {
    selectedCandidates.value = selectedCandidates.value.filter(
      (candidate) => !currentIds.value.has(candidate),
    )
  },
)

function setFilter(value: string): void {
  if (value === 'unadded' || value === 'all') filter.value = value
}

function setCandidate(candidate: string, checked: boolean): void {
  const next = new Set(selectedCandidates.value)
  if (checked) next.add(candidate)
  else next.delete(candidate)
  selectedCandidates.value = [...next]
}

function toggleVisibleCandidates(): void {
  const next = new Set(selectedCandidates.value)
  if (allVisibleSelected.value) {
    selectableVisibleCandidates.value.forEach((candidate) => next.delete(candidate))
  } else {
    selectableVisibleCandidates.value.forEach((candidate) => next.add(candidate))
  }
  selectedCandidates.value = [...next]
}

function confirm(): void {
  if (!selectedCandidates.value.length || props.loading) return
  emit('confirm', [...selectedCandidates.value])
}
</script>

<template>
  <AppDrawer
    :open="open"
    :title="labels.title"
    :description="labels.description"
    :close-label="labels.close"
    :dismissible="dismissible && !loading"
    show-description
    @update:open="emit('update:open', $event)"
  >
    <div class="model-discovery-drawer" :aria-busy="loading ? 'true' : undefined">
      <div v-if="loading" class="model-discovery-drawer__state">
        <QueryFeedback state="loading" :message="labels.loading" />
      </div>
      <div v-else-if="error" class="model-discovery-drawer__state">
        <QueryFeedback
          state="error"
          :message="error"
          :retry-label="labels.retry"
          @retry="emit('retry')"
        />
      </div>
      <template v-else>
        <p class="model-discovery-drawer__notice">{{ labels.notice }}</p>
        <div class="model-discovery-drawer__filters">
          <label class="model-discovery-drawer__search">
            <span class="sr-only">{{ labels.search }}</span>
            <Search :size="16" aria-hidden="true" />
            <input v-model="search" type="search" autocomplete="off" :placeholder="labels.search" />
          </label>
          <SegmentedControl
            :model-value="filter"
            :label="labels.filterLabel"
            :options="filterOptions"
            size="touch"
            @update:model-value="setFilter"
          />
        </div>

        <fieldset class="model-discovery-drawer__candidate-list">
          <legend class="sr-only">{{ labels.filterLabel }}</legend>
          <label
            v-for="candidate in visibleCandidates"
            :key="candidate"
            class="model-discovery-drawer__candidate"
            :class="{ 'model-discovery-drawer__candidate--added': currentIds.has(candidate) }"
          >
            <input
              type="checkbox"
              :checked="selected.has(candidate)"
              :disabled="currentIds.has(candidate)"
              @change="setCandidate(candidate, ($event.target as HTMLInputElement).checked)"
            />
            <code>{{ candidate }}</code>
            <span>{{ currentIds.has(candidate) ? labels.alreadyAdded : labels.unadded }}</span>
          </label>
          <InlineFeedback v-if="!visibleCandidates.length" tone="warning">
            {{ normalizedCandidates.length ? labels.noMatches : labels.empty }}
          </InlineFeedback>
        </fieldset>
      </template>
    </div>

    <template #footer>
      <div class="model-discovery-drawer__footer">
        <div class="model-discovery-drawer__selection">
          <AppButton
            variant="link"
            size="inline"
            :disabled="loading || !selectableVisibleCandidates.length"
            @click="toggleVisibleCandidates"
          >
            {{ allVisibleSelected ? labels.deselectAll : labels.selectAll }}
          </AppButton>
          <span aria-live="polite">{{ labels.selected(selectedCandidates.length) }}</span>
        </div>
        <div class="model-discovery-drawer__actions">
          <AppButton variant="secondary" :disabled="loading" @click="emit('update:open', false)">
            {{ labels.cancel }}
          </AppButton>
          <AppButton :disabled="loading || !selectedCandidates.length" @click="confirm">
            {{ labels.confirm }}
          </AppButton>
        </div>
      </div>
    </template>
  </AppDrawer>
</template>

<style scoped>
.model-discovery-drawer {
  min-height: 320px;
}

.model-discovery-drawer__state {
  display: grid;
  min-height: 280px;
  align-items: start;
}

.model-discovery-drawer__notice {
  margin: 0 0 var(--space-3);
  color: var(--color-text-muted);
}

.model-discovery-drawer__filters {
  display: grid;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.model-discovery-drawer__search {
  display: flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 0 var(--space-3);
}

.model-discovery-drawer__search:focus-within {
  border-color: var(--color-action);
  box-shadow: var(--focus-ring);
}

.model-discovery-drawer__search input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--color-text);
  font: inherit;
}

.model-discovery-drawer__candidate-list {
  display: grid;
  max-height: 420px;
  margin: 0;
  overflow-y: auto;
  border: 0;
  border-block: 1px solid var(--color-border-subtle);
  padding: 0;
}

.model-discovery-drawer__candidate {
  display: grid;
  min-height: var(--touch-target);
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}

.model-discovery-drawer__candidate + .model-discovery-drawer__candidate {
  border-top: 1px solid var(--color-border-subtle);
}

.model-discovery-drawer__candidate code {
  min-width: 0;
  overflow-wrap: anywhere;
}

.model-discovery-drawer__candidate > span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-discovery-drawer__candidate--added {
  color: var(--color-text-muted);
  cursor: not-allowed;
}

.model-discovery-drawer__footer {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.model-discovery-drawer__selection,
.model-discovery-drawer__actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.model-discovery-drawer__selection :deep(.app-button) {
  min-height: var(--touch-target);
}

@media (max-width: 520px) {
  .model-discovery-drawer__filters {
    align-items: stretch;
  }

  .model-discovery-drawer__footer {
    align-items: stretch;
    flex-direction: column;
  }

  .model-discovery-drawer__selection {
    justify-content: space-between;
  }

  .model-discovery-drawer__actions :deep(.app-button) {
    flex: 1;
    min-height: var(--touch-target);
  }
}
</style>
