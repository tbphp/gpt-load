<script setup lang="ts">
import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Ellipsis } from '@lucide/vue'
import { PaginationList, PaginationListItem, PaginationRoot } from 'reka-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppIcon from './AppIcon.vue'
import AppIconButton from './AppIconButton.vue'
import AppSelect from './AppSelect.vue'

const props = withDefaults(
  defineProps<{
    page: number
    pageSize: number
    pageSizes?: readonly number[]
    pending?: boolean
    disabled?: boolean
    mode: 'total' | 'cursor'
    total?: number
    // 只用于游标模式；总量模式根据 total 计算翻页能力。
    hasPrevious?: boolean
    hasNext?: boolean
  }>(),
  { pageSizes: () => [20, 50, 100], total: undefined, hasPrevious: false, hasNext: false },
)
const emit = defineEmits<{
  'update:page': [page: number]
  'update:pageSize': [size: number]
  previous: []
  next: []
}>()
const { t, n } = useI18n()
const total = computed(() => (props.mode === 'total' ? props.total : undefined))
const pages = computed(() =>
  total.value === undefined ? undefined : Math.max(1, Math.ceil(total.value / props.pageSize)),
)
const currentPage = computed(() =>
  pages.value === undefined ? props.page : Math.max(1, Math.min(props.page, pages.value)),
)
const inactive = computed(() => Boolean(props.pending || props.disabled))
const previous = computed(
  () =>
    !inactive.value &&
    (props.mode === 'cursor'
      ? props.hasPrevious
      : pages.value !== undefined && currentPage.value > 1),
)
const next = computed(
  () =>
    !inactive.value &&
    (props.mode === 'cursor'
      ? props.hasNext
      : pages.value !== undefined && currentPage.value < pages.value),
)
const sizes = computed(() =>
  props.pageSizes.map((size) => ({ value: String(size), label: n(size) })),
)
const range = computed(() => {
  if (total.value === undefined) return '—'
  return t('ui.pagination.range', {
    from: n(total.value ? (currentPage.value - 1) * props.pageSize + 1 : 0),
    to: n(Math.min(currentPage.value * props.pageSize, total.value)),
    total: n(total.value),
  })
})
function selectPage(value: number): void {
  if (inactive.value || props.mode !== 'total' || pages.value === undefined) return
  const target = Math.max(1, Math.min(value, pages.value))
  if (target !== currentPage.value) emit('update:page', target)
}
function move(direction: -1 | 1): void {
  if (!(direction < 0 ? previous.value : next.value)) return
  if (props.mode === 'total') selectPage(currentPage.value + direction)
  else if (direction < 0) emit('previous')
  else emit('next')
}
function selectSize(value: string): void {
  const size = Number(value)
  if (!inactive.value && props.pageSizes.includes(size) && size !== props.pageSize) {
    emit('update:pageSize', size)
  }
}
</script>

<template>
  <div class="modern-pagination-container">
    <nav
      class="modern-pagination"
      :aria-label="t('ui.pagination.label')"
      :aria-busy="pending || undefined"
    >
      <span class="modern-pagination-summary" aria-live="polite" aria-atomic="true">{{
        mode === 'total' ? range : t('ui.pagination.current', { page: n(currentPage) })
      }}</span>
      <div class="modern-pagination-controls">
        <div class="modern-pagination-size">
          <AppSelect
            :label="t('ui.pagination.pageSize')"
            :tooltip="false"
            label-hidden
            :model-value="String(pageSize)"
            :options="sizes"
            size="sm"
            :disabled="inactive"
            @update:model-value="selectSize"
          >
            <template #value="{ label }">{{ label }}</template>
          </AppSelect>
        </div>
        <span class="modern-pagination-divider" aria-hidden="true" />
        <PaginationRoot
          v-if="mode === 'total'"
          as="div"
          class="modern-pagination-navigation"
          :page="currentPage"
          :total="total ?? 0"
          :items-per-page="pageSize"
          :sibling-count="1"
          :disabled="inactive || pages === undefined"
          show-edges
          @update:page="selectPage"
        >
          <AppIconButton
            class="modern-pagination-compact"
            :icon="ChevronsLeft"
            :label="t('ui.pagination.first')"
            :tooltip="false"
            size="sm"
            :disabled="!previous"
            @click="selectPage(1)"
          />
          <AppIconButton
            :icon="ChevronLeft"
            :label="t('ui.pagination.previous')"
            :tooltip="false"
            size="sm"
            :disabled="!previous"
            @click="move(-1)"
          />
          <PaginationList
            v-if="pages !== undefined"
            v-slot="{ items }"
            class="modern-pagination-pages"
          >
            <template
              v-for="(item, index) in items"
              :key="item.type === 'page' ? item.value : 'gap-' + index"
            >
              <PaginationListItem v-if="item.type === 'page'" :value="item.value" as-child>
                <AppButton
                  size="sm"
                  :variant="item.value === currentPage ? 'brand' : 'ghost'"
                  :aria-current="item.value === currentPage ? 'page' : undefined"
                  :aria-label="
                    t(item.value === currentPage ? 'ui.pagination.current' : 'ui.pagination.goTo', {
                      page: n(item.value),
                    })
                  "
                  >{{ n(item.value) }}</AppButton
                >
              </PaginationListItem>
              <span v-else class="modern-pagination-ellipsis">
                <AppIcon :icon="Ellipsis" size="sm" />
              </span>
            </template>
          </PaginationList>
          <span v-else class="modern-pagination-unavailable">—</span>
          <span class="modern-pagination-compact modern-pagination-position"
            >{{ n(currentPage) }} / {{ pages === undefined ? '—' : n(pages) }}</span
          >
          <AppIconButton
            :icon="ChevronRight"
            :label="t('ui.pagination.next')"
            :tooltip="false"
            size="sm"
            :disabled="!next"
            @click="move(1)"
          />
          <AppIconButton
            class="modern-pagination-compact"
            :icon="ChevronsRight"
            :label="t('ui.pagination.last')"
            :tooltip="false"
            size="sm"
            :disabled="!next"
            @click="selectPage(pages!)"
          />
        </PaginationRoot>
        <div v-else class="modern-pagination-navigation">
          <AppIconButton
            :icon="ChevronLeft"
            :label="t('ui.pagination.previous')"
            :tooltip="false"
            size="sm"
            :disabled="!previous"
            @click="move(-1)"
          />
          <AppIconButton
            :icon="ChevronRight"
            :label="t('ui.pagination.next')"
            :tooltip="false"
            size="sm"
            :disabled="!next"
            @click="move(1)"
          />
        </div>
      </div>
    </nav>
  </div>
</template>

<style scoped>
.modern-pagination-container {
  container: modern-pagination / inline-size;
  flex: none;
  min-width: 0;
}
.modern-pagination {
  display: grid;
  flex: none;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--modern-space-3) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
  padding: var(--modern-space-4) 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
  text-align: left;
}
.modern-pagination-summary {
  min-width: 0;
}
.modern-pagination-controls,
.modern-pagination-navigation,
.modern-pagination-pages {
  display: flex;
  align-items: center;
}
.modern-pagination-controls {
  flex-wrap: nowrap;
  min-width: 0;
  justify-content: flex-end;
  gap: var(--modern-space-3);
}
.modern-pagination-size {
  flex: none;
}
.modern-pagination-divider {
  width: var(--modern-line-width);
  height: var(--modern-space-5);
  background: var(--modern-border);
}
.modern-pagination-navigation {
  flex: none;
  gap: var(--modern-space-1);
}
.modern-pagination-pages {
  gap: var(--modern-space-0-5);
}
.modern-pagination-ellipsis,
.modern-pagination-unavailable {
  display: inline-flex;
  min-width: var(--modern-control-sm);
  min-height: var(--modern-control-sm);
  align-items: center;
  justify-content: center;
}
.modern-pagination-position {
  min-width: calc(2 * var(--modern-control-sm));
  justify-content: center;
  white-space: nowrap;
}
.modern-pagination-compact {
  display: none;
}
@container modern-pagination (max-width: 640px) {
  .modern-pagination {
    column-gap: var(--modern-space-3);
  }
  .modern-pagination-controls {
    gap: var(--modern-space-2);
  }
  .modern-pagination-pages,
  .modern-pagination-divider,
  .modern-pagination-unavailable {
    display: none;
  }
  .modern-pagination-compact {
    display: inline-flex;
  }
}
@container modern-pagination (max-width: 460px) {
  .modern-pagination {
    grid-template-columns: minmax(0, 1fr);
    row-gap: var(--modern-space-2);
  }
  .modern-pagination-controls {
    justify-self: end;
  }
}
</style>
