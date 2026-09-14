<script setup lang="ts">
import { ListX, RefreshCw, Search } from '@lucide/vue'
import { DialogRoot } from 'reka-ui'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelCandidate } from '@modern/api/model-discovery'
import {
  AppButton,
  AppCheckbox,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppFilterSummary,
  AppIconButton,
  AppListFrame,
  AppNotice,
  AppOverflowText,
  AppPagination,
  AppSelect,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import { useURLState, positivePage } from '@modern/app/url-state'
import { useLoadingActivity, useLoadingFeedback } from '@modern/components/ui/loading'
import ModelSourceBadges from './ModelSourceBadges.vue'
import ModelPriceBadge from './ModelPriceBadge.vue'

const props = defineProps<{
  candidates: readonly ModelCandidate[]
  currentIds: readonly string[]
  loading: boolean
  error?: string
}>()
const emit = defineEmits<{ close: []; refresh: []; confirm: [models: ModelCandidate[]] }>()
const { t, n } = useI18n()
const view = useURLState(
  ['picker_q', 'picker_source', 'picker_price', 'picker_scope', 'picker_page', 'picker_size'],
  (query) => ({
    q: typeof query.picker_q === 'string' ? query.picker_q : '',
    source: ['live', 'catalog'].includes(String(query.picker_source))
      ? String(query.picker_source)
      : 'all',
    price: ['configured', 'pending'].includes(String(query.picker_price))
      ? String(query.picker_price)
      : 'all',
    scope: query.picker_scope === 'all' ? 'all' : 'unadded',
    page: positivePage(query.picker_page),
    size: [20, 50, 100].includes(Number(query.picker_size)) ? Number(query.picker_size) : 20,
  }),
  (value) => ({
    ...(value.q ? { picker_q: value.q } : {}),
    ...(value.source !== 'all' ? { picker_source: value.source } : {}),
    ...(value.price !== 'all' ? { picker_price: value.price } : {}),
    ...(value.scope !== 'unadded' ? { picker_scope: value.scope } : {}),
    ...(value.page > 1 ? { picker_page: String(value.page) } : {}),
    ...(value.size !== 20 ? { picker_size: String(value.size) } : {}),
  }),
)
const search = computed({
  get: () => view.value.q,
  set: (value) => {
    view.value.q = value
    view.value.page = 1
  },
})
const source = computed({
  get: () => view.value.source,
  set: (value) => {
    view.value.source = value
    view.value.page = 1
  },
})
const pricing = computed({
  get: () => view.value.price,
  set: (value) => {
    view.value.price = value
    view.value.page = 1
  },
})
const scope = computed({
  get: () => view.value.scope,
  set: (value) => {
    view.value.scope = value
    view.value.page = 1
  },
})
const page = computed({
  get: () => view.value.page,
  set: (value) => {
    view.value.page = value
  },
})
const pageSize = computed({
  get: () => view.value.size,
  set: (value) => {
    view.value.size = value
    view.value.page = 1
  },
})
const selected = ref(new Set<string>())
const searchInput = ref<InstanceType<typeof AppTextField>>()
const frame = ref<InstanceType<typeof AppListFrame>>()
const checkboxes = new Map<string, { focus(): void }>()
const filtering = ref(false)
const feedback = useLoadingFeedback(filtering)
const current = computed(() => new Set(props.currentIds.map((id) => id.trim())))
const sources = computed(() =>
  ['all', 'live', 'catalog'].map((value) => ({
    value,
    label: t(`modelSelection.sourceOptions.${value}`),
  })),
)
const prices = computed(() =>
  ['all', 'configured', 'pending'].map((value) => ({
    value,
    label: t(`modelSelection.priceOptions.${value}`),
  })),
)
const scopes = computed(() =>
  ['unadded', 'all'].map((value) => ({ value, label: t(`modelSelection.scopeOptions.${value}`) })),
)
const filtered = computed(() => {
  const terms = search.value.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  return props.candidates.filter((candidate) => {
    const text = `${candidate.id} ${candidate.name}`.toLocaleLowerCase()
    return (
      terms.every((term) => text.includes(term)) &&
      (source.value === 'all' || candidate.sources.some((item) => item === source.value)) &&
      (pricing.value === 'all' || candidate.pricingStatus === pricing.value) &&
      (scope.value === 'all' || !current.value.has(candidate.id))
    )
  })
})
const eligible = computed(() =>
  filtered.value.filter((candidate) => !current.value.has(candidate.id)),
)
const selectedVisible = computed(
  () => eligible.value.filter((candidate) => selected.value.has(candidate.id)).length,
)
const allSelected = computed(
  () => eligible.value.length > 0 && selectedVisible.value === eligible.value.length,
)
const rows = computed(() =>
  filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
const filters = computed(() => [
  ...(search.value.trim()
    ? [{ key: 'search', label: t('modelSelection.searchLabel'), value: search.value.trim() }]
    : []),
  ...(source.value !== 'all'
    ? [
        {
          key: 'source',
          label: t('modelSelection.source'),
          value: sources.value.find((item) => item.value === source.value)!.label,
        },
      ]
    : []),
  ...(pricing.value !== 'all'
    ? [
        {
          key: 'pricing',
          label: t('modelSelection.pricing'),
          value: prices.value.find((item) => item.value === pricing.value)!.label,
        },
      ]
    : []),
  ...(scope.value !== 'unadded'
    ? [
        {
          key: 'scope',
          label: t('modelSelection.scope'),
          value: t('modelSelection.scopeOptions.all'),
        },
      ]
    : []),
])

watch([search, source, pricing, scope, page, pageSize], async () => {
  filtering.value = true
  await nextTick()
  frame.value?.scrollToTop()
  filtering.value = false
})
watch(
  () => [props.candidates, props.currentIds] as const,
  () => {
    const valid = new Set(props.candidates.map((item) => item.id))
    selected.value = new Set(
      [...selected.value].filter((id) => valid.has(id) && !current.value.has(id)),
    )
  },
  { immediate: true },
)
watch(
  () => filtered.value.length,
  (length) => {
    page.value = Math.min(page.value, Math.max(1, Math.ceil(length / pageSize.value)))
  },
)
function removeFilter(key: string): void {
  if (key === 'search') search.value = ''
  if (key === 'source') source.value = 'all'
  if (key === 'pricing') pricing.value = 'all'
  if (key === 'scope') scope.value = 'unadded'
}
function resetFilters(): void {
  filters.value.forEach((item) => removeFilter(item.key))
}
function toggle(id: string, value: boolean): void {
  if (props.loading || current.value.has(id)) return
  const next = new Set(selected.value)
  if (value) next.add(id)
  else next.delete(id)
  selected.value = next
}
function toggleFiltered(value: boolean): void {
  if (props.loading) return
  const next = new Set(selected.value)
  eligible.value.forEach((candidate) =>
    value ? next.add(candidate.id) : next.delete(candidate.id),
  )
  selected.value = next
}
function checkboxRef(id: string, element: unknown): void {
  if (element && typeof element === 'object' && 'focus' in element)
    checkboxes.set(id, element as { focus(): void })
  else checkboxes.delete(id)
}
function navigate(event: KeyboardEvent, id?: string, offset = 1): void {
  if (event.isComposing || props.loading) return
  event.preventDefault()
  const available = rows.value.filter((candidate) => !current.value.has(candidate.id))
  const index =
    id === undefined ? 0 : available.findIndex((candidate) => candidate.id === id) + offset
  const candidate = available[Math.max(0, Math.min(index, available.length - 1))]
  if (candidate) checkboxes.get(candidate.id)?.focus()
}
function confirm(): void {
  if (props.loading || !selected.value.size) return
  emit(
    'confirm',
    props.candidates.filter(
      (candidate) => selected.value.has(candidate.id) && !current.value.has(candidate.id),
    ),
  )
}
useLoadingActivity(() => filtering.value || props.loading)
</script>

<template>
  <DialogRoot
    :open="true"
    @update:open="
      (value) => {
        if (!value) emit('close')
      }
    "
  >
    <AppDialogContent
      size="wide"
      :title="t('modelSelection.title')"
      :description="t('modelSelection.title')"
      @open-auto-focus.prevent="searchInput?.focus()"
    >
      <AppDialogHeader
        :title="t('modelSelection.title')"
        :close-label="t('ui.close')"
        @close="emit('close')"
      />
      <div class="modern-model-selection">
        <div class="modern-model-selection-toolbar">
          <AppTextField
            ref="searchInput"
            v-model="search"
            :label="t('modelSelection.searchLabel')"
            label-hidden
            :placeholder="t('modelSelection.searchPlaceholder')"
            :icon="Search"
            :loading="feedback"
            type="search"
            @keydown.down="navigate($event)"
          />
          <div class="modern-model-selection-filters">
            <AppSelect
              v-model="source"
              :label="t('modelSelection.source')"
              label-hidden
              :options="sources"
            />
            <AppSelect
              v-model="pricing"
              :label="t('modelSelection.pricing')"
              label-hidden
              :options="prices"
            />
            <AppSelect
              v-model="scope"
              :label="t('modelSelection.scope')"
              label-hidden
              :options="scopes"
            />
          </div>
          <AppIconButton
            :icon="RefreshCw"
            :label="t('modelSelection.refresh')"
            variant="default"
            :loading="loading"
            @click="emit('refresh')"
          />
        </div>
        <AppFilterSummary :items="filters" @remove="removeFilter" @reset="resetFilters" />
        <AppNotice v-if="error" tone="warning">{{ error }}</AppNotice>
        <AppListFrame
          ref="frame"
          :label="t('modelSelection.title')"
          :loading="loading || filtering"
        >
          <template #header>
            <div class="modern-model-selection-header">
              <AppTooltip
                :label="t('modelSelection.selectFiltered', { count: n(eligible.length) })"
              >
                <AppCheckbox
                  :model-value="allSelected"
                  :indeterminate="selectedVisible > 0 && !allSelected"
                  :label="t('modelSelection.selectFiltered', { count: n(eligible.length) })"
                  label-hidden
                  :disabled="loading || !eligible.length"
                  @update:model-value="toggleFiltered"
                />
              </AppTooltip>
              <div class="modern-model-selection-columns modern-model-selection-labels">
                <span>{{ t('modelSelection.model') }}</span>
                <span>{{ t('modelSelection.source') }}</span>
                <span>{{ t('modelSelection.pricing') }}</span>
              </div>
            </div>
          </template>
          <AppCollectionState
            v-if="!rows.length && !loading"
            :title="t(error ? 'modelSelection.unavailable' : 'modelSelection.empty')"
          />
          <AppCheckbox
            v-for="candidate in rows"
            :key="candidate.id"
            :ref="(element) => checkboxRef(candidate.id, element)"
            class="modern-model-option"
            :class="{ 'is-selected': selected.has(candidate.id) }"
            :model-value="current.has(candidate.id) || selected.has(candidate.id)"
            :label="t('modelSelection.selectModel', { name: candidate.id })"
            :disabled="loading || current.has(candidate.id)"
            @update:model-value="toggle(candidate.id, $event)"
            @keydown.down="navigate($event, candidate.id, 1)"
            @keydown.up="navigate($event, candidate.id, -1)"
            @keydown.enter.prevent="toggle(candidate.id, !selected.has(candidate.id))"
          >
            <span class="modern-model-selection-columns">
              <span class="modern-model-selection-identity">
                <AppOverflowText :text="candidate.id" />
                <small v-if="current.has(candidate.id)">{{ t('modelSelection.added') }}</small>
                <small v-else-if="candidate.name !== candidate.id"
                  ><AppOverflowText :text="candidate.name"
                /></small>
              </span>
              <ModelSourceBadges
                class="modern-model-selection-sources"
                :sources="candidate.sources"
              />
              <ModelPriceBadge :candidate="candidate" />
            </span>
          </AppCheckbox>
          <template #footer>
            <AppPagination
              v-model:page="page"
              v-model:page-size="pageSize"
              mode="total"
              :total="candidates.length || !loading ? filtered.length : undefined"
              :pending="loading"
            />
          </template>
        </AppListFrame>
      </div>
      <footer class="modern-model-selection-footer">
        <div class="modern-model-selection-summary">
          <span aria-live="polite">{{
            t('modelSelection.selected', { count: n(selected.size) })
          }}</span>
          <AppIconButton
            :icon="ListX"
            :label="t('modelSelection.clear')"
            size="sm"
            :disabled="loading || !selected.size"
            @click="selected = new Set()"
          />
        </div>
        <div class="modern-model-selection-actions">
          <AppButton @click="emit('close')">{{ t('ui.cancel') }}</AppButton>
          <AppButton
            class="modern-model-selection-confirm"
            variant="primary"
            :disabled="loading || !selected.size"
            @click="confirm"
            >{{ t('modelSelection.confirm') }}</AppButton
          >
        </div>
      </footer>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-model-selection {
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: min(640px, 68dvh);
  gap: var(--modern-space-3);
  padding: var(--modern-space-4) var(--modern-space-6) 0;
}
.modern-model-selection-toolbar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-model-selection-filters {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  width: 390px;
  gap: var(--modern-space-2);
}
.modern-model-selection-columns {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 180px 132px;
  gap: var(--modern-space-4);
  align-items: center;
  text-align: left;
}
.modern-model-selection-header {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  padding: var(--modern-space-1) var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-subtle);
}
.modern-model-selection-labels {
  flex: 1;
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-selection .modern-model-option {
  width: 100%;
  padding: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-model-option:hover:not(.is-disabled) {
  background: var(--modern-control-hover);
}
.modern-model-option.is-selected {
  background: var(--modern-accent-soft);
}
.modern-model-selection-identity {
  display: flex;
  flex-direction: column;
  min-width: 0;
  gap: var(--modern-space-1);
  font-size: var(--modern-font-size-body);
}
.modern-model-selection-identity small {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-selection-footer {
  display: flex;
  flex: none;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3) var(--modern-space-6);
  border-top: var(--modern-line-width) solid var(--modern-border);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-model-selection-summary {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  font-variant-numeric: tabular-nums;
}
.modern-model-selection-actions {
  display: flex;
  gap: var(--modern-space-2);
  margin-left: auto;
}
.modern-model-selection-confirm {
  min-width: 112px;
}
@media (max-width: 760px) {
  .modern-model-selection-toolbar {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .modern-model-selection-filters {
    grid-column: 1 / -1;
    grid-row: 2;
    width: auto;
  }
  .modern-model-selection-columns {
    grid-template-columns: minmax(0, 1fr) 112px;
    gap: var(--modern-space-2);
  }
  .modern-model-selection-sources {
    grid-column: 1 / -1;
    grid-row: 2;
  }
  .modern-model-selection-labels > :not(:first-child) {
    display: none;
  }
}
</style>
