<script setup lang="ts">
import { ChevronDown, ChevronUp, GripVertical, RotateCcw, TriangleAlert, X } from '@lucide/vue'
import { DialogRoot } from 'reka-ui'
import { computed, nextTick, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import {
  AppButton,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppIcon,
  AppIconButton,
  AppNotice,
  AppOverflowText,
  AppTooltip,
} from '@modern/components/ui'
import ClientCatalogPicker from './ClientCatalogPicker.vue'
import ModelProfileForm from './ModelProfileForm.vue'
import { useClientCatalogEditor } from './use-client-catalog-editor'
import { useClientCatalogSort } from './use-client-catalog-sort'

const emit = defineEmits<{ close: [] }>()
const { t, n } = useI18n()
const guard = ref<InstanceType<typeof AppDraftGuard>>()
const {
  query,
  base,
  selected,
  drafts,
  profiles,
  dirty,
  saving,
  saveError,
  budget,
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
const expanded = ref('')
const viewport = ref<HTMLElement>()
const list = ref<HTMLElement>()
const announcement = ref('')
const panelPrefix = useId()
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
    announcement.value = t('modelManager.clientCatalog.moved', { model, position: n(position) })
  },
})
const options = computed(() =>
  (base.value?.models ?? [])
    .map((model) => model.clientModel)
    .filter((name) => !selected.value.includes(name)),
)
const overflow = computed(() =>
  budget.value ? Math.max(0, selected.value.length - budget.value.includedCount) : 0,
)
const outsideSet = computed(() => new Set(editedOutside.value))
const allRows = computed(() => [...selected.value, ...editedOutside.value])
function context(name: string): string {
  const draft = drafts.value[name]
  const value = draft?.custom.context_window
    ? Number(draft.values.context_window)
    : profiles.value.get(name)?.automatic.context_window
  return value && Number.isSafeInteger(value) ? n(value) : '—'
}
function panelId(name: string): string {
  return `${panelPrefix}-${encodeURIComponent(name)}`
}
function toggle(name: string): void {
  expanded.value = expanded.value === name ? '' : name
}
function removeModel(name: string): void {
  remove(name)
  if (expanded.value === name && !outsideSet.value.has(name)) expanded.value = ''
}
async function submit(): Promise<void> {
  const invalid = await save()
  if (!invalid) return
  expanded.value = invalid
  await nextTick()
  const row = viewport.value?.querySelector<HTMLElement>(`[data-model="${CSS.escape(invalid)}"]`)
  row?.scrollIntoView({ block: 'nearest' })
  ;(
    row?.querySelector<HTMLElement>('input[aria-invalid="true"], [aria-invalid="true"]') ?? row
  )?.focus({ preventScroll: true })
}
async function close(): Promise<void> {
  if (!saving.value && (await guard.value?.confirm())) emit('close')
}
function escape(event: Event): void {
  if (sort.active.value) {
    event.preventDefault()
    sort.cancel()
  } else if (saving.value) event.preventDefault()
}
</script>

<template>
  <DialogRoot :open="true" @update:open="!$event && close()">
    <AppDialogContent
      size="wide"
      :title="t('modelManager.clientCatalog.title')"
      :description="t('modelManager.clientCatalog.help')"
      @escape-key-down="escape"
      @interact-outside="(saving || sort.active.value) && $event.preventDefault()"
    >
      <AppDialogHeader
        :title="t('modelManager.clientCatalog.title')"
        :description="t('modelManager.clientCatalog.help')"
        :close-label="t('ui.close')"
        :close-disabled="saving || sort.active.value"
        @close="close"
      />
      <AppCollectionState
        v-if="!base"
        :loading="query.isPending.value"
        :error="query.isError.value"
        :title="
          t(
            query.isPending.value
              ? 'modelManager.clientCatalog.loading'
              : 'modelManager.clientCatalog.failed',
          )
        "
        ><AppButton v-if="query.isError.value" @click="query.refetch()">{{
          t('ui.retry')
        }}</AppButton></AppCollectionState
      >
      <form v-else class="modern-client-catalog-form" novalidate @submit.prevent="submit">
        <div class="modern-client-catalog-toolbar">
          <span>{{
            t('modelManager.clientCatalog.selectedCount', { count: n(selected.length) })
          }}</span
          ><ClientCatalogPicker
            :names="options"
            :disabled="saving || sort.active.value"
            @add="add"
          />
        </div>
        <div v-if="overflow || previewError || saveError" class="modern-client-catalog-notices">
          <AppNotice v-if="overflow" tone="warning">{{
            t('modelManager.clientCatalog.capacityWarning', { count: n(overflow) })
          }}</AppNotice>
          <AppNotice v-if="previewError" tone="warning"
            >{{ t('modelManager.clientCatalog.previewFailed')
            }}<template #actions
              ><AppButton variant="text" size="xxs" @click="schedulePreview">{{
                t('ui.retry')
              }}</AppButton></template
            ></AppNotice
          >
          <AppNotice v-if="saveError" tone="danger">{{ saveError }}</AppNotice>
        </div>
        <div class="modern-client-catalog-columns" aria-hidden="true">
          <span></span><span>{{ t('modelManager.clientCatalog.modelColumn') }}</span
          ><span>{{ t('modelManager.profile.fields.contextWindow') }}</span
          ><span></span>
        </div>
        <div ref="viewport" class="modern-client-catalog-scroll">
          <AppCollectionState
            v-if="!allRows.length"
            :title="t('modelManager.clientCatalog.empty')"
            :description="t('modelManager.clientCatalog.emptyHelp')"
          />
          <div
            ref="list"
            class="modern-client-catalog-list"
            :class="{ 'is-sorting': sort.active.value }"
            role="list"
            :aria-label="t('modelManager.clientCatalog.title')"
          >
            <div
              v-if="sort.active.value"
              class="modern-client-catalog-placeholder"
              :style="sort.placeholder.value"
              aria-hidden="true"
            ></div>
            <div
              v-for="(name, index) in allRows"
              :key="name"
              :data-model="name"
              :data-sort-row="!outsideSet.has(name) ? '' : undefined"
              role="listitem"
              tabindex="-1"
              class="modern-client-catalog-row"
              :class="{
                'is-expanded': expanded === name,
                'is-overflow': budget && index >= budget.includedCount && !outsideSet.has(name),
                'is-lifted': sort.active.value && sort.name.value === name,
                'has-errors': Object.keys(fieldErrors(name)).length,
                'is-outside': outsideSet.has(name),
              }"
              :style="outsideSet.has(name) ? undefined : sort.style(index)"
            >
              <div
                v-if="outsideSet.has(name) && index === selected.length"
                class="modern-client-catalog-outside-label"
              >
                {{ t('modelManager.clientCatalog.editedOutside') }}
              </div>
              <div class="modern-client-catalog-row-heading">
                <AppIconButton
                  v-if="!outsideSet.has(name)"
                  :icon="GripVertical"
                  :label="t('modelManager.clientCatalog.drag', { model: name })"
                  size="xs"
                  class="modern-client-catalog-grip"
                  data-sort-handle
                  :disabled="saving"
                  :tooltip="!sort.active.value"
                  @pointerdown="sort.start($event, name)"
                  @keydown="sort.keyboard($event, name)"
                  @lostpointercapture="sort.cancel"
                />
                <span v-else></span>
                <div class="modern-client-catalog-row-name">
                  <AppOverflowText :text="name" /><AppTooltip
                    v-if="
                      Object.keys(fieldErrors(name)).length ||
                      (budget && index >= budget.includedCount && !outsideSet.has(name))
                    "
                    :label="
                      t(
                        Object.keys(fieldErrors(name)).length
                          ? 'modelManager.clientCatalog.invalid'
                          : 'modelManager.clientCatalog.overflow',
                      )
                    "
                    ><span
                      tabindex="0"
                      class="modern-client-catalog-status"
                      :class="{ 'is-error': Object.keys(fieldErrors(name)).length }"
                      ><AppIcon :icon="TriangleAlert" size="xs" /></span
                  ></AppTooltip>
                </div>
                <span class="modern-client-catalog-context">{{ context(name) }}</span>
                <div class="modern-client-catalog-row-actions">
                  <AppIconButton
                    :icon="expanded === name ? ChevronUp : ChevronDown"
                    :label="t('modelManager.clientCatalog.editAttributes', { model: name })"
                    :aria-expanded="expanded === name"
                    :aria-controls="panelId(name)"
                    size="xs"
                    :disabled="saving || sort.active.value"
                    @click="toggle(name)"
                  />
                  <AppIconButton
                    v-if="!outsideSet.has(name)"
                    :icon="X"
                    :label="t('modelManager.clientCatalog.remove', { model: name })"
                    size="xs"
                    :disabled="saving || sort.active.value"
                    @click="removeModel(name)"
                  />
                  <AppIconButton
                    v-else
                    :icon="RotateCcw"
                    :label="t('modelManager.profile.resetAll')"
                    size="xs"
                    :disabled="saving"
                    @click="resetProfile(name)"
                  />
                </div>
              </div>
              <div
                v-if="expanded === name"
                :id="panelId(name)"
                class="modern-client-catalog-properties"
              >
                <ModelProfileForm
                  v-model="drafts[name]!"
                  :profile="profiles.get(name)!"
                  :disabled="saving"
                  :errors="fieldErrors(name)"
                />
                <div class="modern-client-catalog-properties-footer">
                  <AppButton
                    variant="text"
                    size="xxs"
                    :icon="RotateCcw"
                    :disabled="saving"
                    @click="resetProfile(name)"
                    >{{ t('modelManager.profile.resetAll') }}</AppButton
                  >
                </div>
              </div>
            </div>
          </div>
        </div>
        <footer class="modern-client-catalog-footer">
          <AppTooltip :label="t('modelManager.clientCatalog.rule')"
            ><AppButton
              variant="text"
              size="sm"
              :icon="RotateCcw"
              :disabled="saving || sort.active.value"
              @click="restoreDirectory"
              >{{ t('modelManager.clientCatalog.restore') }}</AppButton
            ></AppTooltip
          >
          <div class="modern-client-catalog-footer-actions">
            <AppButton size="sm" :disabled="saving || sort.active.value" @click="close">{{
              t('ui.cancel')
            }}</AppButton
            ><AppButton
              type="submit"
              variant="primary"
              size="sm"
              :disabled="saving || !dirty || sort.active.value"
              :loading="saving"
              >{{ t('modelManager.clientCatalog.save') }}</AppButton
            >
          </div>
        </footer>
      </form>
      <span class="modern-sr-only" aria-live="polite">{{ announcement }}</span>
    </AppDialogContent>
  </DialogRoot>
  <AppDraftGuard ref="guard" :dirty="dirty" :pending="saving" />
</template>

<style scoped>
.modern-client-catalog-form {
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.modern-client-catalog-toolbar {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  padding: var(--modern-space-3) var(--modern-space-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-client-catalog-notices {
  display: grid;
  gap: var(--modern-space-2);
  padding: 0 var(--modern-space-5) var(--modern-space-3);
}
.modern-client-catalog-columns,
.modern-client-catalog-row-heading {
  display: grid;
  grid-template-columns: var(--modern-control-xs) minmax(0, 1fr) minmax(100px, max-content) auto;
  gap: var(--modern-space-2);
  align-items: center;
}
.modern-client-catalog-columns {
  padding: var(--modern-space-2) var(--modern-space-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-client-catalog-columns > :last-child {
  width: calc(var(--modern-control-xs) * 2 + var(--modern-space-1));
}
.modern-client-catalog-columns > :nth-child(3) {
  text-align: right;
}
.modern-client-catalog-scroll {
  overflow-y: auto;
  min-height: 0;
  overscroll-behavior: contain;
  padding: var(--modern-space-1) var(--modern-space-5);
}
.modern-client-catalog-list {
  position: relative;
  isolation: isolate;
}
.modern-client-catalog-row {
  position: relative;
  border: var(--modern-line-width) solid transparent;
  border-bottom-color: var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
}
.modern-client-catalog-row-heading {
  min-height: var(--modern-touch-target);
}
.modern-client-catalog-row:hover {
  background: var(--modern-subtle);
}
.modern-client-catalog-row.is-expanded {
  border-color: var(--modern-segmented-active-border);
  background: var(--modern-subtle);
}
.modern-client-catalog-row.is-expanded > .modern-client-catalog-row-heading {
  background: var(--modern-accent-soft);
  border-radius: var(--modern-radius-control) var(--modern-radius-control) 0 0;
}
.modern-client-catalog-row.is-expanded .modern-client-catalog-row-name {
  color: var(--modern-accent);
}
.modern-client-catalog-row-name {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
}
.modern-client-catalog-row-name > :first-child {
  min-width: 0;
}
.modern-client-catalog-context {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
  text-align: right;
  white-space: nowrap;
}
.modern-client-catalog-row-actions,
.modern-client-catalog-footer-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-client-catalog-grip {
  cursor: grab;
  touch-action: none;
}
.modern-client-catalog-status {
  display: inline-flex;
  color: var(--modern-warning);
}
.modern-client-catalog-status.is-error {
  color: var(--modern-danger);
}
.modern-client-catalog-row.has-errors {
  border-color: var(--modern-danger);
}
.modern-client-catalog-row.is-overflow:not(.is-expanded) .modern-client-catalog-row-name {
  color: var(--modern-muted);
}
.modern-client-catalog-properties {
  margin-left: calc(var(--modern-control-xs) + var(--modern-space-2));
  padding: var(--modern-space-2) var(--modern-space-3) var(--modern-space-3) 0;
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-client-catalog-properties-footer {
  display: flex;
  justify-content: flex-end;
  padding-top: var(--modern-space-3);
}
.modern-client-catalog-placeholder {
  position: absolute;
  inset-inline: 0;
  border: var(--modern-line-width) dashed var(--modern-accent);
  border-radius: var(--modern-radius-control);
  background: var(--modern-accent-soft);
  pointer-events: none;
}
.modern-client-catalog-list.is-sorting {
  user-select: none;
}
.modern-client-catalog-list.is-sorting .modern-client-catalog-row {
  pointer-events: none;
}
.modern-client-catalog-list.is-sorting .modern-client-catalog-row:not(.is-lifted) {
  transition: transform var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-client-catalog-row.is-lifted {
  z-index: var(--modern-layer-header);
  border-color: var(--modern-accent);
  box-shadow: var(--modern-shadow-menu);
  background: var(--modern-surface);
  transition: none;
  cursor: grabbing;
}
.modern-client-catalog-outside-label {
  padding-top: var(--modern-space-4);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-client-catalog-footer {
  display: flex;
  flex: none;
  justify-content: space-between;
  align-items: center;
  gap: var(--modern-space-2);
  padding: var(--modern-space-3) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
@media (max-width: 760px) {
  .modern-client-catalog-toolbar,
  .modern-client-catalog-columns,
  .modern-client-catalog-scroll,
  .modern-client-catalog-footer {
    padding-inline: var(--modern-space-3);
  }
  .modern-client-catalog-notices {
    padding-inline: var(--modern-space-3);
  }
  .modern-client-catalog-columns,
  .modern-client-catalog-row-heading {
    grid-template-columns: var(--modern-control-xs) minmax(0, 1fr) auto auto;
    gap: var(--modern-space-1);
  }
  .modern-client-catalog-properties {
    margin-left: var(--modern-space-3);
  }
}
</style>
