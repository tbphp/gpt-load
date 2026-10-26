<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { Search, SlidersHorizontal } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { ModelPriceSource } from '@modern/api/models'
import {
  AppButton,
  AppCheckbox,
  AppCollectionState,
  AppFilterSummary,
  AppListFrame,
  AppOverflowText,
  AppPagination,
  AppSearchSelect,
  AppSelect,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import ModelPriceSummary from './ModelPriceSummary.vue'

const props = defineProps<{ items: ModelPriceSource[]; loading: boolean; disabled: boolean }>()
const selected = defineModel<number[]>({ required: true })
const { t } = useI18n()
const filtersOpen = ref(false)
const filtersId = useId()
const search = ref('')
const selectedOnly = ref(selected.value.length > 0)
const group = ref('')
const status = ref('all')
const filterCount = computed(() => Number(Boolean(group.value)) + Number(status.value !== 'all'))
const page = ref(1)
const pageSize = ref(20)
const frame = ref<InstanceType<typeof AppListFrame>>()
const groupOptions = computed(() => [
  { value: '', label: t('modelManager.allGroups') },
  ...[
    ...new Map(
      props.items.flatMap((item) => item.groups).map((group) => [group.id, group]),
    ).values(),
  ].map((group) => ({ value: String(group.id), label: group.name })),
])
const statusOptions = computed(() =>
  ['all', 'pending', 'configured'].map((value) => ({
    value,
    label: t('modelManager.' + (value === 'all' ? 'allPrices' : value)),
  })),
)
const activeFilters = computed(() => [
  ...(group.value
    ? [
        {
          key: 'group',
          label: t('modelManager.groupUnit'),
          value: groupOptions.value.find((option) => option.value === group.value)?.label ?? '',
        },
      ]
    : []),
  ...(status.value !== 'all'
    ? [
        {
          key: 'status',
          label: t('modelManager.pricingStatus'),
          value: t('modelManager.' + status.value),
        },
      ]
    : []),
])
function resetFilters(): void {
  group.value = ''
  status.value = 'all'
}
function removeFilter(key: string): void {
  if (key === 'group') group.value = ''
  if (key === 'status') status.value = 'all'
}
const filtered = computed(() => {
  const terms = search.value.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  return props.items.filter((item) => {
    const text = [
      item.price.channel.name,
      item.price.model,
      ...item.aliases,
      ...item.groups.map((group) => group.name),
    ]
      .join(' ')
      .toLocaleLowerCase()
    return (
      terms.every((term) => text.includes(term)) &&
      (!group.value || item.groups.some((item) => String(item.id) === group.value)) &&
      (status.value === 'all' || item.price.status === status.value) &&
      (!selectedOnly.value || selected.value.includes(item.price.id))
    )
  })
})
const rows = computed(() =>
  filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
const pageSelected = computed(
  () => rows.value.filter((item) => selected.value.includes(item.price.id)).length,
)
const allPageSelected = computed(
  () => rows.value.length > 0 && pageSelected.value === rows.value.length,
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
  if (!checked) selected.value = selected.value.filter((id) => !ids.has(id))
  else selected.value = [...new Set([...selected.value, ...ids])].slice(0, 100)
}
watch([search, group, status, selectedOnly, pageSize], () => {
  page.value = 1
})
watch([page, search, group, status, selectedOnly, pageSize], () => frame.value?.scrollToTop())
watch(filtered, (rows) => {
  page.value = Math.min(page.value, Math.max(1, Math.ceil(rows.length / pageSize.value)))
})
</script>

<template>
  <div class="modern-price-targets">
    <div class="modern-price-targets-search">
      <AppTextField
        v-model="search"
        :disabled="disabled"
        :icon="Search"
        :label="t('modelManager.search')"
        :placeholder="t('modelManager.reuse.search')"
        label-hidden
        type="search"
        size="sm"
      />
      <AppButton
        :variant="filtersOpen || filterCount ? 'brand' : 'ghost'"
        size="sm"
        :icon="SlidersHorizontal"
        :disabled="disabled"
        :aria-expanded="filtersOpen"
        :aria-controls="filtersId"
        @click="filtersOpen = !filtersOpen"
      >
        {{ t('modelManager.reuse.filters')
        }}<template v-if="filterCount"> · {{ filterCount }}</template>
      </AppButton>
    </div>
    <div v-show="filtersOpen" :id="filtersId" class="modern-price-targets-filters">
      <AppSearchSelect
        v-model="group"
        :disabled="disabled"
        :options="groupOptions"
        :label="t('modelManager.groupUnit')"
        label-hidden
        size="sm"
      />
      <AppSelect
        v-model="status"
        :disabled="disabled"
        :options="statusOptions"
        :label="t('modelManager.pricingStatus')"
        label-hidden
        size="sm"
      />
    </div>
    <AppFilterSummary
      v-if="!filtersOpen"
      :items="activeFilters"
      :disabled="disabled"
      @remove="removeFilter"
      @reset="resetFilters"
    />
    <div class="modern-price-targets-tools">
      <AppTooltip :label="t('modelManager.reuse.selectPage')">
        <AppCheckbox
          :model-value="allPageSelected"
          :indeterminate="pageSelected > 0 && !allPageSelected"
          :disabled="disabled || !rows.length || (selected.length >= 100 && pageSelected === 0)"
          label-hidden
          :label="t('modelManager.reuse.selectPage')"
          @update:model-value="togglePage"
        />
      </AppTooltip>
      <span class="modern-price-targets-divider" aria-hidden="true" />
      <AppButton
        :variant="selectedOnly ? 'brand' : 'default'"
        size="xs"
        :disabled="disabled"
        :aria-pressed="selectedOnly"
        @click="selectedOnly = !selectedOnly"
        >{{ t('modelManager.reuse.selectedOnly') }}</AppButton
      >
      <AppButton
        v-if="selected.length"
        variant="text"
        size="xs"
        :disabled="disabled"
        @click="clearSelection"
        >{{ t('modelManager.reuse.clear') }}</AppButton
      >
    </div>
    <p v-if="selected.length >= 100" class="modern-price-targets-limit" role="status">
      {{ t('modelManager.reuse.limitReached') }}
    </p>
    <AppListFrame ref="frame" :label="t('modelManager.reuse.chooseTargets')" :loading="loading">
      <AppCollectionState v-if="!rows.length" :icon="Search" :title="t('modelManager.noMatches')" />
      <AppCheckbox
        v-for="item in rows"
        :key="item.price.id"
        class="modern-price-target"
        :class="{ 'is-selected': selected.includes(item.price.id) }"
        :label="item.price.channel.name + ' · ' + item.price.model"
        :model-value="selected.includes(item.price.id)"
        :disabled="disabled || (!selected.includes(item.price.id) && selected.length >= 100)"
        @update:model-value="toggle(item.price.id, $event)"
      >
        <span class="modern-price-target-identity">
          <span class="modern-price-target-title">
            <AppOverflowText class="modern-price-target-name" :text="item.price.model" />
            <AppOverflowText class="modern-price-target-channel" :text="item.price.channel.name" />
          </span>
          <ModelPriceSummary
            columns
            class="modern-price-target-prices"
            :prices="item.price.prices"
          />
        </span>
      </AppCheckbox>
      <template #footer>
        <AppPagination
          v-model:page="page"
          v-model:page-size="pageSize"
          compact
          mode="total"
          :pending="disabled"
          :total="filtered.length"
        />
      </template>
    </AppListFrame>
  </div>
</template>

<style scoped>
.modern-price-targets {
  container: price-targets / inline-size;
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  gap: var(--modern-space-2);
  padding: var(--modern-space-3) var(--modern-space-4) 0;
}
.modern-price-targets-search,
.modern-price-targets-tools {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-price-targets-search > :first-child {
  flex: 1;
  min-width: 0;
}
.modern-price-targets-tools {
  flex-wrap: wrap;
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding: 0 var(--modern-space-2) var(--modern-space-2);
}
.modern-price-targets-divider {
  height: var(--modern-space-4);
  width: var(--modern-line-width);
  background: var(--modern-border);
  margin-inline: var(--modern-space-1);
}
.modern-price-targets-filters {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-2);
}
.modern-price-target {
  width: 100%;
  padding: var(--modern-space-2);
  border-bottom: var(--modern-line-width) solid
    color-mix(in srgb, var(--modern-border) 60%, transparent);
}
.modern-price-target:hover:not(.is-disabled) {
  background: var(--modern-control-hover);
}
.modern-price-target.is-selected {
  background: var(--modern-accent-soft);
}
.modern-price-target-identity {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1);
}
.modern-price-target-title {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-price-target-name {
  flex: 1;
  min-width: 0;
  font-weight: var(--modern-weight-medium);
}
.modern-price-target-channel {
  max-width: 35%;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-price-target-prices {
  justify-content: flex-start;
}
.modern-price-targets-limit {
  margin: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
</style>
