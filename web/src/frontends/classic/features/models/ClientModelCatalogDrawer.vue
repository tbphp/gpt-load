<script setup lang="ts">
import { ChevronDown, ChevronUp, GripVertical, RotateCcw, X } from '@lucide/vue'
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import ClientModelProfileForm from './ClientModelProfileForm.vue'
import { useClientCatalogEditor } from './use-client-catalog-editor'
import { useClientCatalogSort } from './use-client-catalog-sort'
const emit = defineEmits<{ close: [] }>()
const { t, n } = useI18n()
const {
  query,
  base,
  selected,
  drafts,
  profiles,
  dirty,
  saving,
  saved,
  saveError,
  budget,
  previewing,
  previewError,
  editedOutside,
  fieldErrors,
  restoreDirectory,
  add,
  remove,
  move,
  resetProfile,
  schedulePreview,
  save,
} = useClientCatalogEditor()
const guard = useUnsavedChanges(dirty, { blocked: saving })
const expanded = ref('')
const viewport = ref<HTMLElement>()
const list = ref<HTMLElement>()
const announcement = ref('')
const sort = useClientCatalogSort({
  names: selected,
  viewport,
  list,
  disabled: saving,
  collapse: () => {
    expanded.value = ''
  },
  move,
  announce: (model, position) => {
    announcement.value = t('models.clientCatalog.moved', { model, position: n(position) })
  },
})
const overflow = computed(() =>
  budget.value ? Math.max(0, budget.value.entries.length - budget.value.includedCount) : 0,
)
const outsideSet = computed(() => new Set(editedOutside.value))
const allRows = computed(() => [...selected.value, ...editedOutside.value])
const picking = ref(false)
const search = ref('')
const picked = ref<string[]>([])
const results = computed(() =>
  (base.value?.models ?? []).filter(
    (model) =>
      !selected.value.includes(model.clientModel) &&
      search.value
        .toLocaleLowerCase()
        .trim()
        .split(/\s+/)
        .every((term) => label(model.clientModel).toLocaleLowerCase().includes(term)),
  ),
)
function label(name: string): string {
  const display = drafts.value[name]?.values.display_name.trim()
  return display && display !== name ? `${display}（${name}）` : name
}
function selectResults(): void {
  picked.value = [...new Set([...picked.value, ...results.value.map((model) => model.clientModel)])]
}
function pick(name: string, event: Event): void {
  picked.value = (event.target as HTMLInputElement).checked
    ? [...new Set([...picked.value, name])]
    : picked.value.filter((value) => value !== name)
}
function confirmAdd(): void {
  add(picked.value)
  picking.value = false
  picked.value = []
  search.value = ''
}
async function close(): Promise<void> {
  if (!saving.value && !sort.active.value && (await guard.confirmDiscard())) emit('close')
}
async function submit(): Promise<void> {
  const invalid = await save()
  if (!invalid) return
  expanded.value = invalid
  await nextTick()
  viewport.value
    ?.querySelector<HTMLElement>(`[data-model="${CSS.escape(invalid)}"]`)
    ?.scrollIntoView({ block: 'nearest' })
}
</script>
<template>
  <AppDrawer
    :open="true"
    size="wide"
    :title="t('models.clientCatalog.title')"
    :description="t('models.clientCatalog.help')"
    show-description
    :close-label="t('common.close')"
    :dismissible="!saving && !sort.active.value"
    @update:open="!$event && close()"
  >
    <template #filters>
      <div class="catalog-controls">
        <div class="catalog-toolbar">
          <span>{{ t('models.clientCatalog.selectedCount', { count: n(selected.length) }) }}</span
          ><AppButton
            variant="secondary"
            size="xs"
            :disabled="saving || sort.active.value || !base"
            @click="picking = !picking"
            >{{ t('models.clientCatalog.addModels') }}</AppButton
          >
        </div>
        <AsyncRefreshIndicator
          :active="query.isFetching.value || previewing || saving"
          :label="t('models.clientCatalog.loading')"
        />
        <InlineFeedback v-if="overflow" tone="warning">{{
          t('models.clientCatalog.capacityWarning', { count: n(overflow) })
        }}</InlineFeedback>
        <InlineFeedback v-if="previewError" tone="warning"
          >{{ t('models.clientCatalog.previewFailed')
          }}<template #action
            ><AppButton variant="link" size="inline" :disabled="saving" @click="schedulePreview">{{
              t('common.retry')
            }}</AppButton></template
          ></InlineFeedback
        >
        <InlineFeedback v-if="saveError" tone="danger">{{ saveError }}</InlineFeedback>
        <InlineFeedback v-if="saved" tone="success">{{
          t('models.clientCatalog.saveSuccess')
        }}</InlineFeedback>
      </div>
    </template>
    <div v-if="picking" class="catalog-picker">
      <AppSearchInput
        v-model="search"
        size="xs"
        :label="t('models.clientCatalog.addLabel')"
        :placeholder="t('models.clientCatalog.search')"
        :clear-label="t('models.filters.clearSearch')"
        :disabled="saving"
      />
      <p>{{ t('models.clientCatalog.resultCount', { count: n(results.length) }) }}</p>
      <div class="catalog-picker__results">
        <label v-for="model in results" :key="model.clientModel" class="catalog-picker__option"
          ><input
            type="checkbox"
            :checked="picked.includes(model.clientModel)"
            :disabled="saving"
            @change="pick(model.clientModel, $event)"
          /><OverflowTooltip :content="label(model.clientModel)">{{
            label(model.clientModel)
          }}</OverflowTooltip></label
        >
      </div>
      <div class="catalog-toolbar">
        <AppButton
          variant="secondary"
          size="xs"
          :disabled="saving || results.every((model) => picked.includes(model.clientModel))"
          @click="selectResults"
          >{{ t('models.clientCatalog.selectResults') }}</AppButton
        ><AppButton size="xs" :disabled="saving || !picked.length" @click="confirmAdd">{{
          t('models.clientCatalog.add', { count: n(picked.length) })
        }}</AppButton>
      </div>
    </div>
    <InlineFeedback v-if="query.isError.value && !base" tone="danger"
      >{{ t('models.clientCatalog.failed')
      }}<template #action
        ><AppButton variant="link" size="inline" @click="query.refetch()">{{
          t('common.retry')
        }}</AppButton></template
      ></InlineFeedback
    >
    <p v-else-if="!base">{{ t('models.clientCatalog.loading') }}</p>
    <div v-else ref="viewport" class="catalog-viewport">
      <p v-if="!allRows.length">{{ t('models.clientCatalog.emptyHelp') }}</p>
      <div
        ref="list"
        class="catalog-list"
        role="list"
        :aria-label="t('models.clientCatalog.title')"
        :class="{ 'is-sorting': sort.active.value }"
      >
        <div
          v-if="sort.active.value"
          class="catalog-placeholder"
          :style="sort.placeholder.value"
          aria-hidden="true"
        ></div>
        <div
          v-for="(name, index) in allRows"
          :key="name"
          :data-model="name"
          :data-sort-row="!outsideSet.has(name) ? '' : undefined"
          class="catalog-row"
          :class="{
            'is-lifted': sort.active.value && sort.name.value === name,
            'is-expanded': expanded === name,
            'has-errors': Object.keys(fieldErrors(name)).length > 0,
            'is-overflow': budget && index >= budget.includedCount && !outsideSet.has(name),
          }"
          :style="outsideSet.has(name) ? undefined : sort.style(index)"
          role="listitem"
        >
          <p v-if="outsideSet.has(name) && index === selected.length">
            {{ t('models.clientCatalog.editedOutside') }}
          </p>
          <div class="catalog-row__heading">
            <AppTooltip
              v-if="!outsideSet.has(name)"
              :content="t('models.clientCatalog.drag', { model: name })"
              :disabled="sort.active.value"
              ><IconButton
                variant="ghost"
                size="xxs"
                :label="t('models.clientCatalog.drag', { model: name })"
                :disabled="saving"
                class="catalog-grip"
                data-sort-handle
                @pointerdown="sort.start($event, name)"
                @keydown="sort.keyboard($event, name)"
                @lostpointercapture="sort.cancel"
                ><GripVertical :size="15" /></IconButton></AppTooltip
            ><span v-else></span>
            <OverflowTooltip :content="label(name)" class="catalog-name">{{
              label(name)
            }}</OverflowTooltip>
            <span class="catalog-context">{{
              n(Number(drafts[name]?.values.context_window) || 0)
            }}</span>
            <AppTooltip :content="t('models.clientCatalog.editAttributes', { model: name })"
              ><IconButton
                variant="ghost"
                :label="t('models.clientCatalog.editAttributes', { model: name })"
                size="xxs"
                :aria-expanded="expanded === name"
                :disabled="saving || sort.active.value"
                @click="expanded = expanded === name ? '' : name"
                ><ChevronUp v-if="expanded === name" :size="16" /><ChevronDown
                  v-else
                  :size="16" /></IconButton
            ></AppTooltip>
            <AppTooltip
              :content="
                outsideSet.has(name)
                  ? t('models.profile.resetAll')
                  : t('models.clientCatalog.remove', { model: name })
              "
              ><IconButton
                variant="ghost"
                :label="
                  outsideSet.has(name)
                    ? t('models.profile.resetAll')
                    : t('models.clientCatalog.remove', { model: name })
                "
                size="xxs"
                :disabled="saving || sort.active.value"
                @click="outsideSet.has(name) ? resetProfile(name) : remove(name)"
                ><RotateCcw v-if="outsideSet.has(name)" :size="15" /><X
                  v-else
                  :size="15" /></IconButton
            ></AppTooltip>
          </div>
          <div v-if="expanded === name" class="catalog-properties">
            <ClientModelProfileForm
              v-model="drafts[name]!"
              :profile="profiles.get(name)!"
              :errors="fieldErrors(name)"
              :disabled="saving"
            /><AppButton
              v-if="Object.values(drafts[name]!.custom).some(Boolean)"
              class="catalog-properties-reset"
              variant="link"
              size="xs"
              :disabled="saving"
              @click="resetProfile(name)"
              >{{ t('models.profile.resetAll') }}</AppButton
            >
          </div>
        </div>
      </div>
    </div>
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <template #footer
      ><AppTooltip :content="t('models.clientCatalog.rule')"
        ><AppButton
          variant="link"
          size="xs"
          :disabled="saving || sort.active.value || !base"
          @click="restoreDirectory"
          >{{ t('models.clientCatalog.restore') }}</AppButton
        ></AppTooltip
      ><AppButton
        variant="secondary"
        size="xs"
        :disabled="saving || sort.active.value"
        @click="close"
        >{{ t('common.cancel') }}</AppButton
      ><AppButton
        size="xs"
        :busy="saving"
        :disabled="!dirty || sort.active.value"
        @click="submit"
        >{{ t('models.clientCatalog.save') }}</AppButton
      ></template
    >
  </AppDrawer>
</template>
<style scoped>
.catalog-controls {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--color-surface-sunken);
  border-bottom: 1px solid var(--color-border-subtle);
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}
.catalog-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-3);
}
.catalog-picker {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  margin-bottom: var(--space-3);
}
.catalog-picker__results {
  max-height: 220px;
  overflow-y: auto;
}
.catalog-picker__option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  min-width: 0;
}
.catalog-viewport {
  max-height: 60dvh;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.catalog-list {
  position: relative;
  isolation: isolate;
}
.catalog-row {
  position: relative;
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface);
}
.catalog-row__heading {
  display: grid;
  grid-template-columns:
    var(--control-xxs) minmax(0, 1fr) max-content var(--control-xxs)
    var(--control-xxs);
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
}
.catalog-name {
  font-size: var(--text-meta);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.catalog-context {
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
  color: var(--color-text-muted);
}
.catalog-grip {
  touch-action: none;
  cursor: grab;
}
.catalog-properties {
  padding: var(--space-2) var(--space-3) var(--space-3);
  background: var(--color-surface-sunken);
  border-top: 1px solid var(--color-border-subtle);
  display: grid;
  gap: var(--space-2);
}
.catalog-row.is-expanded {
  border: 1px solid color-mix(in srgb, var(--color-action) 28%, var(--color-border-subtle));
  border-radius: var(--radius-control);
  overflow: hidden;
}
.catalog-row.is-expanded .catalog-row__heading {
  background: var(--color-action-soft);
}
.catalog-row.is-expanded .catalog-name {
  color: var(--color-action);
}
.catalog-properties-reset {
  justify-self: end;
}
.catalog-placeholder {
  position: absolute;
  inset-inline: 0;
  border: 1px dashed var(--color-action);
  background: var(--color-action-soft);
  pointer-events: none;
}
.catalog-list.is-sorting {
  user-select: none;
}
.catalog-list.is-sorting .catalog-row {
  pointer-events: none;
}
.catalog-list.is-sorting .catalog-row:not(.is-lifted) {
  transition: transform var(--duration-fast) var(--easing-standard);
}
.catalog-row.has-errors {
  border: 1px solid var(--color-danger);
}
.catalog-row.is-overflow:not(.is-expanded) .catalog-name {
  color: var(--color-text-faint);
}
.catalog-row.is-lifted {
  z-index: 1;
  box-shadow: var(--shadow-overlay);
}
@media (max-width: 860px) {
  .catalog-row__heading {
    grid-template-columns: var(--touch-target) minmax(0, 1fr) max-content var(--touch-target) var(
        --touch-target
      );
  }
}
</style>
