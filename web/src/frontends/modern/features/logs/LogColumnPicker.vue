<script setup lang="ts">
import { Columns3, Search } from '@lucide/vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppCheckbox, AppIconButton, AppTextField } from '@modern/components/ui'
import { matchesSearchOption } from '@modern/components/ui/search-options'
import { overlaySideOffset } from '@modern/components/ui/overlay'
import type { LogColumn, LogColumnId } from './log-columns'

const props = defineProps<{ columns: readonly LogColumn[]; selected: readonly LogColumnId[] }>()
defineEmits<{ toggle: [id: LogColumnId, checked: boolean]; reset: []; all: [] }>()
const { t, n } = useI18n()
const search = ref('')
const sections = computed(() =>
  (['request', 'models', 'routing', 'result', 'performance', 'tokens', 'billing'] as const)
    .map((id) => ({
      id,
      columns: props.columns.filter(
        (column) =>
          column.section === id &&
          matchesSearchOption(
            { value: column.id, label: t('logs.columns.' + column.id) },
            search.value,
          ),
      ),
    }))
    .filter((section) => section.columns.length),
)
</script>

<template>
  <PopoverRoot>
    <PopoverTrigger as-child>
      <AppIconButton :icon="Columns3" :label="t('logs.columnSettings')" :tooltip="true" />
    </PopoverTrigger>
    <PopoverPortal
      ><PopoverContent
        class="modern-log-column-picker"
        align="end"
        :side-offset="overlaySideOffset"
        :collision-padding="16"
      >
        <div class="modern-log-column-heading">
          <strong>{{ t('logs.columnSettings') }}</strong
          ><span>{{ t('logs.columnCount', { count: n(selected.length) }) }}</span>
        </div>
        <AppTextField
          v-model="search"
          :label="t('logs.searchColumns')"
          label-hidden
          :placeholder="t('logs.searchColumns')"
          :icon="Search"
          type="search"
          size="sm"
        />
        <div class="modern-log-column-options">
          <section v-for="section in sections" :key="section.id">
            <h4>{{ t('logs.sections.' + section.id) }}</h4>
            <div>
              <AppCheckbox
                v-for="column in section.columns"
                :key="column.id"
                :label="t('logs.columns.' + column.id)"
                :model-value="selected.includes(column.id)"
                :disabled="selected.length === 1 && selected.includes(column.id)"
                @update:model-value="$emit('toggle', column.id, $event)"
              />
            </div>
          </section>
          <p v-if="!sections.length">{{ t('ui.select.empty') }}</p>
        </div>
        <footer>
          <AppButton variant="ghost" size="xs" @click="$emit('all')">{{
            t('logs.showAllColumns')
          }}</AppButton
          ><AppButton variant="brand" size="xs" @click="$emit('reset')">{{
            t('logs.defaultColumns')
          }}</AppButton>
        </footer>
      </PopoverContent></PopoverPortal
    >
  </PopoverRoot>
</template>

<style>
.modern-log-column-picker {
  z-index: var(--modern-layer-menu);
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-3);
  width: min(420px, calc(100vw - var(--modern-space-8)));
  max-height: min(620px, calc(100dvh - var(--modern-space-8)));
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  box-shadow: var(--modern-shadow-menu);
  padding: var(--modern-space-4);
}
.modern-log-column-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-log-column-heading span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-log-column-options {
  display: grid;
  gap: var(--modern-space-4);
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding-block: var(--modern-space-1);
}
.modern-log-column-options h4 {
  margin-bottom: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-log-column-options section > div {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-2);
}
.modern-log-column-picker footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-2);
}
</style>
