<script setup lang="ts">
import { computed } from 'vue'

import SkeletonBlock from './SkeletonBlock.vue'

const props = withDefaults(
  defineProps<{
    label: string
    variant?: 'page' | 'collection' | 'dashboard' | 'detail' | 'form' | 'panel'
    rows?: number
    columns?: number
    rowHeight?: string
    mobileRowHeight?: string
    minHeight?: string
    showPagination?: boolean
    showControls?: boolean
    concealed?: boolean
  }>(),
  {
    variant: 'panel',
    rows: 5,
    columns: 5,
    rowHeight: '72px',
    mobileRowHeight: '164px',
    minHeight: undefined,
    showPagination: true,
    showControls: false,
    concealed: false,
  },
)

const safeRows = computed(() => Math.min(100, Math.max(1, Math.trunc(props.rows))))
const safeColumns = computed(() => Math.min(8, Math.max(1, Math.trunc(props.columns))))
const surfaceStyle = computed<Record<string, string>>(() => {
  const style: Record<string, string> = {
    '--skeleton-row-height': props.rowHeight,
    '--skeleton-mobile-row-height': props.mobileRowHeight,
  }
  if (props.minHeight) style['--skeleton-surface-min-height'] = props.minHeight
  return style
})

function cellWidth(column: number, row: number): string {
  const widths = ['72%', '54%', '64%', '46%', '58%', '42%', '68%', '38%']
  return widths[(column + row) % widths.length] ?? '60%'
}
</script>

<template>
  <section
    class="skeleton-surface"
    :class="[`skeleton-surface--${variant}`, { 'skeleton-surface--concealed': concealed }]"
    :style="surfaceStyle"
    :role="concealed ? undefined : 'status'"
    :aria-label="concealed ? undefined : label"
    :aria-busy="concealed ? undefined : 'true'"
    :aria-hidden="concealed ? 'true' : undefined"
  >
    <span class="sr-only">{{ label }}</span>

    <template v-if="variant === 'collection'">
      <div v-if="showControls" class="skeleton-surface__controls" aria-hidden="true">
        <SkeletonBlock height="76px" />
        <div class="skeleton-surface__filter-row">
          <SkeletonBlock height="52px" />
          <SkeletonBlock height="52px" />
          <SkeletonBlock height="52px" />
        </div>
      </div>
      <div class="skeleton-surface__collection" aria-hidden="true">
        <div class="skeleton-surface__collection-header">
          <SkeletonBlock
            v-for="column in safeColumns"
            :key="`header-${column}`"
            height="10px"
            :width="cellWidth(column, 0)"
          />
        </div>
        <div v-for="row in safeRows" :key="`row-${row}`" class="skeleton-surface__collection-row">
          <SkeletonBlock
            v-for="column in safeColumns"
            :key="`cell-${row}-${column}`"
            height="11px"
            :width="cellWidth(column, row)"
          />
        </div>
      </div>
      <div v-if="showPagination" class="skeleton-surface__pagination" aria-hidden="true">
        <SkeletonBlock width="92px" height="11px" />
        <SkeletonBlock width="136px" height="32px" />
      </div>
    </template>

    <template v-else-if="variant === 'dashboard'">
      <div class="skeleton-surface__metrics" aria-hidden="true">
        <SkeletonBlock v-for="index in 3" :key="index" height="108px" />
      </div>
      <SkeletonBlock height="190px" aria-hidden="true" />
      <div class="skeleton-surface__split" aria-hidden="true">
        <SkeletonBlock height="266px" />
        <SkeletonBlock height="266px" />
      </div>
      <SkeletonBlock height="240px" aria-hidden="true" />
    </template>

    <template v-else-if="variant === 'detail'">
      <SkeletonBlock width="46%" height="32px" aria-hidden="true" />
      <SkeletonBlock height="72px" aria-hidden="true" />
      <SkeletonBlock height="50px" aria-hidden="true" />
      <SkeletonBlock v-for="index in 3" :key="index" height="132px" aria-hidden="true" />
    </template>

    <template v-else-if="variant === 'form'">
      <SkeletonBlock width="42%" height="28px" aria-hidden="true" />
      <div class="skeleton-surface__form" aria-hidden="true">
        <SkeletonBlock v-for="index in 6" :key="index" height="58px" />
      </div>
      <SkeletonBlock height="156px" aria-hidden="true" />
    </template>

    <template v-else-if="variant === 'page'">
      <SkeletonBlock width="38%" height="32px" aria-hidden="true" />
      <div class="skeleton-surface__metrics" aria-hidden="true">
        <SkeletonBlock v-for="index in 3" :key="index" height="108px" />
      </div>
      <SkeletonBlock height="230px" aria-hidden="true" />
      <SkeletonBlock height="180px" aria-hidden="true" />
    </template>

    <template v-else>
      <SkeletonBlock width="44%" height="28px" aria-hidden="true" />
      <SkeletonBlock height="84px" aria-hidden="true" />
      <SkeletonBlock height="148px" aria-hidden="true" />
    </template>
  </section>
</template>

<style scoped>
.skeleton-surface {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: var(--space-4);
}

.skeleton-surface--concealed {
  visibility: hidden;
}

.skeleton-surface--page {
  min-height: var(--skeleton-surface-min-height, 620px);
}

.skeleton-surface--collection {
  min-height: var(--skeleton-surface-min-height, 360px);
  gap: 0;
}

.skeleton-surface--dashboard {
  min-height: var(--skeleton-surface-min-height, 760px);
  gap: var(--space-6);
}

.skeleton-surface--detail {
  min-height: var(--skeleton-surface-min-height, 620px);
}

.skeleton-surface--form {
  min-height: var(--skeleton-surface-min-height, 540px);
}

.skeleton-surface--panel {
  min-height: var(--skeleton-surface-min-height, 320px);
}

.skeleton-surface__collection {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(v-bind(safeColumns), minmax(0, 1fr));
  column-gap: var(--space-4);
  overflow: hidden;
  border-bottom: 1px solid var(--color-border-control);
}

.skeleton-surface__controls {
  display: grid;
  gap: var(--space-3);
  padding-bottom: var(--space-3);
}

.skeleton-surface__filter-row {
  display: grid;
  grid-template-columns: minmax(260px, 1fr) 204px 148px;
  gap: var(--space-2-5);
}

.skeleton-surface__collection-header,
.skeleton-surface__collection-row {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: center;
  column-gap: inherit;
}

.skeleton-surface__collection-header {
  min-height: 38px;
}

.skeleton-surface__collection-row {
  min-height: var(--skeleton-row-height);
  border-top: 1px solid var(--color-border-subtle);
}

.skeleton-surface__pagination {
  display: flex;
  min-height: var(--pagination-min-height);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-2);
}

.skeleton-surface__metrics,
.skeleton-surface__split,
.skeleton-surface__form {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}

.skeleton-surface__metrics {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.skeleton-surface__split,
.skeleton-surface__form {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

@media (max-width: 860px) {
  .skeleton-surface {
    min-height: var(--skeleton-surface-min-height, 0);
  }

  .skeleton-surface__collection {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-2);
    overflow: visible;
    border: 0;
    padding-top: var(--space-2);
  }

  .skeleton-surface__filter-row {
    grid-template-columns: minmax(0, 1fr);
  }

  .skeleton-surface__collection-header {
    display: none;
  }

  .skeleton-surface__collection-row {
    display: grid;
    min-height: var(--skeleton-mobile-row-height);
    grid-column: 1;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-4);
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-control);
    padding: var(--space-4);
  }

  .skeleton-surface__metrics,
  .skeleton-surface__split,
  .skeleton-surface__form {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
