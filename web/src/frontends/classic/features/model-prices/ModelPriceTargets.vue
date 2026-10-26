<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { SlidersHorizontal } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { ModelPriceSourceDto } from '@/app/resources/model-prices'
import AppButton from '@/components/ui/AppButton.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import ModelPriceSummary from './ModelPriceSummary.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'

const props = defineProps<{ items: ModelPriceSourceDto[]; disabled: boolean }>()
const selected = defineModel<number[]>({ required: true })
const { t } = useI18n()
const filtersOpen = ref(false)
const filtersId = useId()
const search = ref('')
const selectedOnly = ref(selected.value.length > 0)
const list = ref<HTMLElement>()
const group = ref('')
const status = ref('all')
const filterCount = computed(() => Number(Boolean(group.value)) + Number(status.value !== 'all'))
const page = ref(1)
const pageSize = ref<20 | 50 | 100>(20)
const groupOptions = computed(() => [
  { value: '', label: t('modelPrices.reuse.allGroups') },
  ...[
    ...new Map(
      props.items.flatMap((item) => item.groups).map((group) => [group.id, group]),
    ).values(),
  ].map((group) => ({ value: String(group.id), label: group.name })),
])
const statusOptions = computed(() => [
  { value: 'all', label: t('modelPrices.reuse.allPrices') },
  ...['pending', 'configured'].map((value) => ({ value, label: t('modelPrices.status.' + value) })),
])
const activeFilterLabels = computed(() =>
  [
    ...(group.value
      ? [groupOptions.value.find((option) => option.value === group.value)?.label ?? '']
      : []),
    ...(status.value !== 'all' ? [t('modelPrices.status.' + status.value)] : []),
  ].join(' · '),
)
function resetFilters(): void {
  group.value = ''
  status.value = 'all'
}
const filtered = computed(() => {
  const terms = search.value.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  return props.items.filter((item) => {
    const text = [
      item.price.channel_name,
      item.price.model_id,
      ...item.aliases,
      ...item.groups.map((group) => group.name),
    ]
      .join(' ')
      .toLocaleLowerCase()
    return (
      terms.every((term) => text.includes(term)) &&
      (!group.value || item.groups.some((item) => String(item.id) === group.value)) &&
      (status.value === 'all' || item.price.pricing_status === status.value) &&
      (!selectedOnly.value || selected.value.includes(item.price.id))
    )
  })
})
const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize.value)))
const rows = computed(() =>
  filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
const pageSelected = computed(
  () => rows.value.filter((item) => selected.value.includes(item.price.id)).length,
)
function clearSelection(): void {
  if (props.disabled) return
  selected.value = []
  selectedOnly.value = false
}
function toggle(id: number, checked: boolean): void {
  if (props.disabled) return
  if (!checked) selected.value = selected.value.filter((value) => value !== id)
  else if (selected.value.length < 100) selected.value = [...new Set([...selected.value, id])]
}
function togglePage(checked: boolean): void {
  if (props.disabled) return
  const ids = new Set(rows.value.map((item) => item.price.id))
  selected.value = checked
    ? [...new Set([...selected.value, ...ids])].slice(0, 100)
    : selected.value.filter((id) => !ids.has(id))
}
watch([search, group, status, selectedOnly, pageSize], () => {
  page.value = 1
})
watch([page, search, group, status, selectedOnly, pageSize], () => list.value?.scrollTo({ top: 0 }))
watch(pages, (pages) => {
  page.value = Math.min(page.value, pages)
})
</script>

<template>
  <div class="price-targets">
    <div class="price-targets-search">
      <AppSearchInput
        v-model="search"
        size="xs"
        :disabled="disabled"
        :label="t('modelPrices.reuse.search')"
        :placeholder="t('modelPrices.reuse.search')"
        :clear-label="t('modelPrices.reuse.clearSearch')"
      />
      <AppButton
        variant="ghost"
        :tone="filtersOpen || filterCount ? 'action' : 'neutral'"
        size="compact"
        :disabled="disabled"
        :aria-expanded="filtersOpen"
        :aria-controls="filtersId"
        @click="filtersOpen = !filtersOpen"
      >
        <SlidersHorizontal :size="16" aria-hidden="true" />{{ t('modelPrices.reuse.filters')
        }}<template v-if="filterCount"> · {{ filterCount }}</template>
      </AppButton>
    </div>
    <div v-show="filtersOpen" :id="filtersId" class="price-targets-filters">
      <AppSelect
        v-model="group"
        size="compact"
        :disabled="disabled"
        :options="groupOptions"
        :label="t('modelPrices.reuse.allGroups')"
      />
      <AppSelect
        v-model="status"
        size="compact"
        :disabled="disabled"
        :options="statusOptions"
        :label="t('modelPrices.reuse.allPrices')"
      />
    </div>
    <div v-if="filterCount && !filtersOpen" class="price-targets-active-filters">
      <OverflowTooltip as="span" :content="activeFilterLabels">{{
        activeFilterLabels
      }}</OverflowTooltip>
      <AppButton variant="link" size="xs" :disabled="disabled" @click="resetFilters">{{
        t('modelPrices.reuse.resetFilters')
      }}</AppButton>
    </div>
    <div class="price-targets-tools">
      <AppTooltip :content="t('modelPrices.reuse.selectPage')">
        <label
          ><input
            type="checkbox"
            :aria-label="t('modelPrices.reuse.selectPage')"
            :checked="rows.length > 0 && pageSelected === rows.length"
            :indeterminate="pageSelected > 0 && pageSelected < rows.length"
            :disabled="disabled || !rows.length || (selected.length >= 100 && pageSelected === 0)"
            @change="togglePage(($event.target as HTMLInputElement).checked)"
        /></label>
      </AppTooltip>
      <span class="price-targets-divider" aria-hidden="true" />
      <AppButton
        variant="secondary"
        :tone="selectedOnly ? 'action' : 'neutral'"
        size="xs"
        :disabled="disabled"
        :aria-pressed="selectedOnly"
        @click="selectedOnly = !selectedOnly"
        >{{ t('modelPrices.reuse.selectedOnly') }}</AppButton
      >
      <AppButton
        v-if="selected.length"
        variant="link"
        size="xs"
        :disabled="disabled"
        @click="clearSelection"
        >{{ t('modelPrices.reuse.clear') }}</AppButton
      >
    </div>
    <p v-if="selected.length >= 100" class="price-targets-limit" role="status">
      {{ t('modelPrices.reuse.limitReached') }}
    </p>
    <div ref="list" class="price-targets-list">
      <p v-if="!rows.length">{{ t('modelPrices.reuse.noMatches') }}</p>
      <label
        v-for="item in rows"
        :key="item.price.id"
        class="price-target"
        :class="{ 'is-selected': selected.includes(item.price.id) }"
      >
        <input
          type="checkbox"
          :checked="selected.includes(item.price.id)"
          :disabled="disabled || (!selected.includes(item.price.id) && selected.length >= 100)"
          @change="toggle(item.price.id, ($event.target as HTMLInputElement).checked)"
        />
        <span class="price-target-identity">
          <span class="price-target-title">
            <OverflowTooltip class="price-target-name" as="span" :content="item.price.model_id">{{
              item.price.model_id
            }}</OverflowTooltip>
            <OverflowTooltip
              class="price-target-channel"
              as="span"
              :content="item.price.channel_name"
              >{{ item.price.channel_name }}</OverflowTooltip
            >
          </span>
          <ModelPriceSummary columns class="price-target-prices" :prices="item.price.prices" />
        </span>
      </label>
    </div>
    <PaginationBar
      v-model:page-size="pageSize"
      :pending="disabled"
      :page="page"
      :total-items="filtered.length"
      :total-pages="pages"
      show-page-size
      @previous="page--"
      @next="page++"
    />
  </div>
</template>

<style scoped>
.price-targets {
  container: classic-price-targets / inline-size;
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
  gap: var(--space-2);
}
.price-targets-search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.price-targets-search > :first-child {
  flex: 1;
  min-width: 0;
}
.price-targets-active-filters {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}
.price-targets-active-filters > :first-child {
  flex: 1;
  min-width: 0;
}
.price-targets-divider {
  height: var(--space-4);
  width: 1px;
  background: var(--color-border-subtle);
  margin-inline: var(--space-1);
}
.price-targets-filters {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}
.price-targets-tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 0 var(--space-2) var(--space-2);
  font-size: var(--text-sm);
}
.price-targets-tools label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}
.price-targets-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.price-target {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  border-bottom: 1px solid color-mix(in srgb, var(--color-border-subtle) 60%, transparent);
  cursor: pointer;
}
.price-target:hover {
  background: var(--color-surface-sunken);
}
.price-target.is-selected {
  background: var(--color-action-soft);
}
.price-target-identity {
  flex: 1;
  display: grid;
  min-width: 0;
  gap: var(--space-1);
}
.price-target-title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}
.price-target-name {
  flex: 1;
  min-width: 0;
  font-weight: 600;
}
.price-target-channel {
  max-width: 35%;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}
.price-target-prices {
  justify-content: flex-start;
  font-size: var(--text-label-xs);
}
.price-targets-limit {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}
@media (max-width: 760px) {
  .price-targets-tools label {
    min-height: var(--touch-target);
  }
}
</style>
