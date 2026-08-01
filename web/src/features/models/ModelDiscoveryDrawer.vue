<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
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
  { value: 'all', label: props.labels.filterAll },
  { value: 'unadded', label: props.labels.filterUnadded },
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
    appearance="ledger"
    :open="open"
    :title="labels.title"
    :description="labels.description"
    :close-label="labels.close"
    :dismissible="dismissible && !loading"
    show-description
    @update:open="emit('update:open', $event)"
  >
    <template #filters>
      <AppSearchInput
        v-model="search"
        class="model-discovery-drawer__search"
        :label="labels.search"
        :placeholder="labels.search"
        :clear-label="labels.clearSearch"
      />
      <SegmentedControl
        :model-value="filter"
        :label="labels.filterLabel"
        :options="filterOptions"
        appearance="drawer"
        @update:model-value="setFilter"
      />
    </template>
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
          <AppButton
            variant="secondary"
            size="sm"
            :disabled="loading"
            @click="emit('update:open', false)"
          >
            {{ labels.cancel }}
          </AppButton>
          <AppButton size="sm" :disabled="loading || !selectedCandidates.length" @click="confirm">
            {{ labels.confirm }}
          </AppButton>
        </div>
      </div>
    </template>
  </AppDrawer>
</template>

<style scoped>
.model-discovery-drawer {
  min-height: 100%;
}

.model-discovery-drawer__search {
  width: auto;
  min-width: 0;
  flex: 1;
}

.model-discovery-drawer__state {
  display: grid;
  min-height: 280px;
  align-items: start;
}

.model-discovery-drawer__candidate-list {
  display: grid;
  margin: 0;
  border: 0;
  padding: 0;
}

.model-discovery-drawer__candidate {
  display: grid;
  min-height: 0;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-3) 1px;
  cursor: pointer;
}

.model-discovery-drawer__candidate input {
  accent-color: var(--color-action);
}

.model-discovery-drawer__candidate code {
  min-width: 0;
  overflow: hidden;
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
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
  min-height: 0;
  padding: 4px 0;
  font-size: var(--text-sm);
  font-weight: 600;
}

@media (max-width: 520px) {
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
