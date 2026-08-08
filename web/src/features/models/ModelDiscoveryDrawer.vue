<script setup lang="ts">
import { CircleDollarSign, Radar, RefreshCw } from '@lucide/vue'
import { computed, ref, toRef, watch } from 'vue'

import { useStableLoading } from '@/app/loading-state'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import type { ModelCandidate } from '@/app/resources/providers'

import type { ModelDiscoveryDrawerLabels } from './model-draft'

export type DiscoveryFilter = 'unadded' | 'all'

const props = withDefaults(
  defineProps<{
    open: boolean
    candidates: readonly ModelCandidate[]
    currentIds: readonly string[]
    loading: boolean
    error: string
    labels: ModelDiscoveryDrawerLabels
    dismissible?: boolean
    search?: string
    filter?: DiscoveryFilter
  }>(),
  { dismissible: true, search: undefined, filter: undefined },
)
const emit = defineEmits<{
  'update:open': [open: boolean]
  'update:search': [search: string]
  'update:filter': [filter: DiscoveryFilter]
  retry: []
  confirm: [candidates: ModelCandidate[]]
}>()
const loadingVisible = useStableLoading(toRef(props, 'loading'))

const internalSearch = ref(props.search ?? '')
const internalFilter = ref<DiscoveryFilter>(props.filter ?? 'unadded')
const searchValue = computed({
  get: () => internalSearch.value,
  set: (value: string) => {
    internalSearch.value = value
    emit('update:search', value)
  },
})
const filterValue = computed({
  get: () => internalFilter.value,
  set: (value: DiscoveryFilter) => {
    internalFilter.value = value
    emit('update:filter', value)
  },
})
watch(
  () => props.search,
  (value) => {
    internalSearch.value = value ?? ''
  },
)
watch(
  () => props.filter,
  (value) => {
    internalFilter.value = value ?? 'unadded'
  },
)
const selectedCandidates = ref<string[]>([])
const normalizedCandidates = computed(() => props.candidates)
const currentIds = computed(
  () => new Set(props.currentIds.map((candidate) => candidate.trim()).filter(Boolean)),
)
const selected = computed(() => new Set(selectedCandidates.value))
const visibleCandidates = computed(() => {
  const query = searchValue.value.trim().toLocaleLowerCase()
  return normalizedCandidates.value.filter(
    (candidate) =>
      (filterValue.value === 'all' || !currentIds.value.has(candidate.id)) &&
      (!query ||
        `${candidate.name} ${candidate.id} ${candidate.sources.join(' ')}`
          .toLocaleLowerCase()
          .includes(query)),
  )
})
const selectableVisibleCandidates = computed(() =>
  visibleCandidates.value.filter((candidate) => !currentIds.value.has(candidate.id)),
)
const allVisibleSelected = computed(
  () =>
    selectableVisibleCandidates.value.length > 0 &&
    selectableVisibleCandidates.value.every((candidate) => selected.value.has(candidate.id)),
)
const filterOptions = computed(() => [
  { value: 'all', label: props.labels.filterAll },
  { value: 'unadded', label: props.labels.filterUnadded },
])

watch(
  () => props.open,
  (open) => {
    if (!open) return
    internalSearch.value = props.search ?? ''
    internalFilter.value = props.filter ?? 'unadded'
    selectedCandidates.value = []
  },
)

watch(
  () => props.candidates,
  () => {
    const valid = new Set(normalizedCandidates.value.map(({ id }) => id))
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
  if (value === 'unadded' || value === 'all') filterValue.value = value
}

function setCandidate(candidate: ModelCandidate, checked: boolean): void {
  const next = new Set(selectedCandidates.value)
  if (checked) next.add(candidate.id)
  else next.delete(candidate.id)
  selectedCandidates.value = [...next]
}

function toggleVisibleCandidates(): void {
  const next = new Set(selectedCandidates.value)
  if (allVisibleSelected.value) {
    selectableVisibleCandidates.value.forEach((candidate) => next.delete(candidate.id))
  } else {
    selectableVisibleCandidates.value.forEach((candidate) => next.add(candidate.id))
  }
  selectedCandidates.value = [...next]
}

function confirm(): void {
  if (!selectedCandidates.value.length || props.loading) return
  const selectedIDs = new Set(selectedCandidates.value)
  emit(
    'confirm',
    normalizedCandidates.value.filter(({ id }) => selectedIDs.has(id)),
  )
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
        v-model="searchValue"
        class="model-discovery-drawer__search"
        :label="labels.search"
        :placeholder="labels.search"
        :clear-label="labels.clearSearch"
      />
      <SegmentedControl
        :model-value="filterValue"
        :label="labels.filterLabel"
        :options="filterOptions"
        appearance="drawer"
        @update:model-value="setFilter"
      />
    </template>
    <div class="model-discovery-drawer" :aria-busy="loading ? 'true' : undefined">
      <SkeletonSurface
        v-if="loading || loadingVisible"
        variant="collection"
        :rows="5"
        :columns="3"
        row-height="58px"
        mobile-row-height="76px"
        min-height="328px"
        :show-pagination="false"
        :concealed="!loadingVisible"
        :label="labels.loading"
      />
      <div v-else-if="error" class="model-discovery-drawer__state">
        <InlineFeedback tone="danger">
          {{ error }}
          <template #action>
            <AppButton variant="link" size="inline" @click="emit('retry')">
              <RefreshCw :size="15" aria-hidden="true" />{{ labels.retry }}
            </AppButton>
          </template>
        </InlineFeedback>
      </div>
      <template v-else>
        <fieldset class="model-discovery-drawer__candidate-list">
          <legend class="sr-only">{{ labels.filterLabel }}</legend>
          <label
            v-for="candidate in visibleCandidates"
            :key="candidate.id"
            class="model-discovery-drawer__candidate"
            :class="{ 'model-discovery-drawer__candidate--added': currentIds.has(candidate.id) }"
          >
            <input
              type="checkbox"
              :checked="currentIds.has(candidate.id) || selected.has(candidate.id)"
              :disabled="currentIds.has(candidate.id)"
              :title="currentIds.has(candidate.id) ? labels.alreadyAdded : undefined"
              @change="setCandidate(candidate, ($event.target as HTMLInputElement).checked)"
            />
            <span class="model-discovery-drawer__identity">
              <OverflowTooltip as="strong" :content="candidate.name" :focusable="false">
                {{ candidate.name }}
              </OverflowTooltip>
              <OverflowTooltip as="code" :content="candidate.id" :focusable="false">
                {{ candidate.id }}
              </OverflowTooltip>
            </span>
            <span class="model-discovery-drawer__evidence">
              <AppTooltip v-if="candidate.sources.includes('live')" :content="labels.sources.live">
                <span
                  class="model-discovery-drawer__status-icon model-discovery-drawer__status-icon--live"
                  role="img"
                  :aria-label="labels.sources.live"
                >
                  <Radar :size="17" stroke-width="1.9" aria-hidden="true" />
                </span>
              </AppTooltip>
              <AppTooltip
                v-if="candidate.pricing_source"
                :content="labels.pricingDiscovered(candidate.pricing_source)"
              >
                <span
                  class="model-discovery-drawer__status-icon model-discovery-drawer__status-icon--pricing"
                  role="img"
                  :aria-label="labels.pricingDiscovered(candidate.pricing_source)"
                >
                  <CircleDollarSign :size="17" stroke-width="1.9" aria-hidden="true" />
                </span>
              </AppTooltip>
            </span>
          </label>
          <InlineFeedback
            v-if="!visibleCandidates.length"
            class="model-discovery-drawer__empty-feedback"
            tone="warning"
          >
            {{ normalizedCandidates.length ? labels.noMatches : labels.empty }}
          </InlineFeedback>
        </fieldset>
      </template>
    </div>

    <template #footer>
      <div class="model-discovery-drawer__footer">
        <div class="model-discovery-drawer__selection">
          <AppButton
            variant="secondary"
            size="compact"
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
            size="compact"
            :disabled="loading"
            @click="emit('update:open', false)"
          >
            {{ labels.cancel }}
          </AppButton>
          <AppButton
            size="compact"
            :disabled="loading || !selectedCandidates.length"
            @click="confirm"
          >
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

.model-discovery-drawer__state > .inline-feedback {
  margin-top: var(--space-3);
  align-items: center;
}

.model-discovery-drawer__state > .inline-feedback :deep(.inline-feedback__action) {
  align-self: center;
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
  grid-template-columns: auto minmax(0, 1fr) minmax(52px, auto);
  align-items: center;
  gap: 10px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-3) 1px;
  cursor: pointer;
}

.model-discovery-drawer__candidate input {
  accent-color: var(--color-action);
}

.model-discovery-drawer__identity {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.model-discovery-drawer__identity strong,
.model-discovery-drawer__identity code {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-discovery-drawer__identity strong {
  font-size: var(--text-sm);
}

.model-discovery-drawer__identity code {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-discovery-drawer__evidence {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
}

.model-discovery-drawer__status-icon {
  display: grid;
  width: 24px;
  height: 24px;
  place-items: center;
  border-radius: var(--radius-control);
}

.model-discovery-drawer__status-icon--live {
  background: var(--color-action-soft);
  color: var(--color-action);
}

.model-discovery-drawer__status-icon--pricing {
  background: var(--color-success-bg);
  color: var(--color-success);
}

.model-discovery-drawer__candidate--added {
  color: var(--color-text-muted);
  cursor: not-allowed;
}

.model-discovery-drawer__empty-feedback {
  margin-top: var(--space-3);
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

@media (max-width: 520px) {
  .model-discovery-drawer__candidate {
    grid-template-columns: auto minmax(0, 1fr);
  }

  .model-discovery-drawer__evidence {
    grid-column: 2;
    justify-content: flex-start;
    flex-wrap: wrap;
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
  }
}
</style>
