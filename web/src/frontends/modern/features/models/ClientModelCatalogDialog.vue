<script setup lang="ts">
import { ArrowDown, ArrowUp, ChevronDown, GripVertical, Plus, RotateCcw, X } from '@lucide/vue'
import { DialogRoot } from 'reka-ui'
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppIconButton,
  AppMultiSelect,
  AppNotice,
  AppOverflowText,
  AppProgressBar,
} from '@modern/components/ui'
import ModelProfileForm from './ModelProfileForm.vue'
import { useClientCatalogEditor } from './use-client-catalog-editor'

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
  previewing,
  previewError,
  invalidModel,
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
const additions = ref<string[]>([])
const dragging = ref('')
const dragOver = ref('')
const list = ref<HTMLElement>()
function rowFor(name: string): HTMLElement | undefined {
  return list.value?.querySelector<HTMLElement>(`[data-model="${CSS.escape(name)}"]`) ?? undefined
}
const options = computed(() =>
  (base.value?.models ?? [])
    .filter((model) => !selected.value.includes(model.clientModel))
    .map((model) => ({ value: model.clientModel, label: model.clientModel })),
)
const overflow = computed(() =>
  budget.value ? selected.value.length - budget.value.includedCount : 0,
)
const capacity = computed(() =>
  budget.value
    ? t('modelManager.clientCatalog.capacity', {
        used: n(budget.value.selectedBytes),
        limit: n(budget.value.limitBytes),
      })
    : t(
        invalidModel.value
          ? 'modelManager.clientCatalog.invalidPreview'
          : 'modelManager.clientCatalog.calculating',
      ),
)
const rowEntries = computed(
  () => new Map(budget.value?.entries.map((entry) => [entry.model, entry]) ?? []),
)
function context(name: string): string {
  const draft = drafts.value[name]
  const profile = profiles.value.get(name)
  const value = draft?.custom.context_window
    ? Number(draft.values.context_window)
    : profile?.automatic.context_window
  return value && Number.isSafeInteger(value)
    ? t('modelManager.clientCatalog.context', { value: n(value) })
    : t('modelManager.profile.unknownContext')
}
function resetDirectory(): void {
  additions.value = []
  restoreDirectory()
}
function addModels(): void {
  add(additions.value)
  additions.value = []
}
function startDrag(event: DragEvent, name: string): void {
  if (saving.value || !event.dataTransfer) {
    event.preventDefault()
    return
  }
  dragging.value = name
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', name)
}
function finishDrag(): void {
  dragging.value = ''
  dragOver.value = ''
}
function drop(event: DragEvent, target: string): void {
  if (!dragging.value || saving.value) return
  if (dragging.value === target) {
    finishDrag()
    return
  }
  const from = selected.value.indexOf(dragging.value)
  const index = selected.value.indexOf(target)
  const rect = rowFor(target)?.getBoundingClientRect()
  const after = rect ? event.clientY > rect.top + rect.height / 2 : false
  move(
    dragging.value,
    Math.max(
      0,
      Math.min(selected.value.length - 1, index + (after ? 1 : 0) - (from < index ? 1 : 0)),
    ),
  )
  finishDrag()
}
async function submit(): Promise<void> {
  const invalid = await save()
  if (!invalid) return
  expanded.value = invalid
  await nextTick()
  const row = rowFor(invalid)
  row?.scrollIntoView({ block: 'nearest' })
  const field = row?.querySelector<HTMLElement>(
    '[aria-invalid="true"] input, input[aria-invalid="true"], [aria-invalid="true"]',
  )
  ;(field ?? row)?.focus({ preventScroll: true })
}
async function close(): Promise<void> {
  if (!saving.value && (await guard.value?.confirm())) emit('close')
}
</script>

<template>
  <DialogRoot :open="true" @update:open="!$event && close()">
    <AppDialogContent
      size="wide"
      class="modern-client-catalog-dialog"
      :title="t('modelManager.clientCatalog.title')"
      :description="t('modelManager.clientCatalog.help')"
      @escape-key-down="saving && $event.preventDefault()"
      @interact-outside="saving && $event.preventDefault()"
    >
      <AppDialogHeader
        :title="t('modelManager.clientCatalog.title')"
        :description="t('modelManager.clientCatalog.help')"
        :close-label="t('ui.close')"
        :close-disabled="saving"
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
      >
        <AppButton v-if="query.isError.value" @click="query.refetch()">{{
          t('ui.retry')
        }}</AppButton>
      </AppCollectionState>
      <form v-else class="modern-client-catalog-form" novalidate @submit.prevent="submit">
        <div class="modern-client-catalog-summary" aria-live="polite" :aria-busy="previewing">
          <div class="modern-client-catalog-summary-line">
            <span>{{
              t('modelManager.clientCatalog.previewScope', { version: base.clientVersion })
            }}</span>
            <strong>{{ capacity }}</strong>
          </div>
          <AppProgressBar
            :label="capacity"
            :value="budget ? (budget.selectedBytes / budget.limitBytes) * 100 : undefined"
            :tone="overflow ? 'warning' : 'success'"
            size="sm"
          />
          <div class="modern-client-catalog-summary-line">
            <span>{{
              budget
                ? t('modelManager.clientCatalog.counts', {
                    shown: n(budget.includedCount),
                    total: n(selected.length),
                  })
                : t('modelManager.clientCatalog.selectedCount', { count: n(selected.length) })
            }}</span>
            <span v-if="budget">{{
              t('modelManager.clientCatalog.responseBytes', { bytes: n(budget.responseBytes) })
            }}</span>
          </div>
          <p>{{ t('modelManager.clientCatalog.previewHelp') }}</p>
          <AppNotice v-if="previewError" tone="warning"
            >{{ t('modelManager.clientCatalog.previewFailed') }}
            <AppButton variant="text" size="xxs" @click="schedulePreview">{{
              t('ui.retry')
            }}</AppButton></AppNotice
          >
          <AppNotice v-if="saveError" tone="danger">{{ saveError }}</AppNotice>
        </div>
        <div ref="list" class="modern-client-catalog-scroll">
          <div class="modern-client-catalog-add">
            <AppMultiSelect
              v-model="additions"
              :options="options"
              :label="t('modelManager.clientCatalog.addLabel')"
              :placeholder="t('modelManager.clientCatalog.search')"
              label-hidden
              :disabled="saving || !options.length"
            />
            <AppButton
              :icon="Plus"
              size="sm"
              :disabled="saving || !additions.length"
              @click="addModels"
              >{{ t('modelManager.clientCatalog.add', { count: additions.length }) }}</AppButton
            >
          </div>
          <p class="modern-client-catalog-rule">{{ t('modelManager.clientCatalog.rule') }}</p>
          <AppCollectionState
            v-if="!selected.length"
            :title="t('modelManager.clientCatalog.empty')"
            :description="t('modelManager.clientCatalog.emptyHelp')"
          />
          <ol
            class="modern-client-catalog-list"
            :aria-label="t('modelManager.clientCatalog.title')"
          >
            <li
              v-for="(name, index) in selected"
              :key="name"
              :data-model="name"
              class="modern-client-catalog-row"
              :class="{
                'is-overflow': budget && index >= budget.includedCount,
                'is-dragging': dragging === name,
                'is-drop-target': dragOver === name && dragging !== name,
                'has-errors': Object.keys(fieldErrors(name)).length,
              }"
              tabindex="-1"
              @dragover.prevent="dragging && (dragOver = name)"
              @drop.prevent="drop($event, name)"
            >
              <div
                v-if="budget && overflow && index === budget.includedCount"
                class="modern-client-catalog-cutoff"
                role="status"
              >
                <span>{{ t('modelManager.clientCatalog.cutoff', { count: n(overflow) }) }}</span>
              </div>
              <div class="modern-client-catalog-row-heading">
                <AppIconButton
                  :icon="GripVertical"
                  :label="t('modelManager.clientCatalog.drag', { model: name })"
                  size="xxs"
                  class="modern-client-catalog-grip"
                  :draggable="!saving"
                  :disabled="saving"
                  @dragstart="startDrag($event, name)"
                  @dragend="finishDrag"
                />
                <span class="modern-client-catalog-position">{{ index + 1 }}</span>
                <div class="modern-client-catalog-row-name">
                  <AppOverflowText :text="name" /><small
                    >{{ context(name)
                    }}<template v-if="rowEntries.get(name)">
                      · {{ n(rowEntries.get(name)!.bytes) }} B</template
                    ></small
                  >
                </div>
                <AppBadge
                  v-if="Object.keys(fieldErrors(name)).length"
                  tone="danger"
                  variant="plain"
                  size="xs"
                  >{{ t('modelManager.clientCatalog.invalid') }}</AppBadge
                >
                <AppBadge
                  v-else-if="budget && index >= budget.includedCount"
                  tone="warning"
                  variant="plain"
                  size="xs"
                  >{{ t('modelManager.clientCatalog.overflow') }}</AppBadge
                >
                <div class="modern-client-catalog-row-actions">
                  <AppIconButton
                    :icon="ArrowUp"
                    :label="t('modelManager.clientCatalog.moveUp', { model: name })"
                    size="xxs"
                    :disabled="saving || index === 0"
                    @click="move(name, index - 1)"
                  />
                  <AppIconButton
                    :icon="ArrowDown"
                    :label="t('modelManager.clientCatalog.moveDown', { model: name })"
                    size="xxs"
                    :disabled="saving || index === selected.length - 1"
                    @click="move(name, index + 1)"
                  />
                  <AppButton
                    variant="text"
                    size="xxs"
                    :icon="ChevronDown"
                    :aria-expanded="expanded === name"
                    :disabled="saving"
                    @click="expanded = expanded === name ? '' : name"
                    >{{ t('modelManager.clientCatalog.attributes') }}</AppButton
                  >
                  <AppIconButton
                    :icon="X"
                    :label="t('modelManager.clientCatalog.remove', { model: name })"
                    size="xxs"
                    :disabled="saving"
                    @click="remove(name)"
                  />
                </div>
              </div>
              <div
                v-if="expanded === name && profiles.get(name) && drafts[name]"
                class="modern-client-catalog-properties"
              >
                <div class="modern-client-catalog-properties-heading">
                  <span>{{ t('modelManager.clientCatalog.attributesHelp') }}</span
                  ><AppButton
                    variant="text"
                    size="xxs"
                    :icon="RotateCcw"
                    :disabled="saving"
                    @click="resetProfile(name)"
                    >{{ t('modelManager.profile.resetAll') }}</AppButton
                  >
                </div>
                <ModelProfileForm
                  v-model="drafts[name]!"
                  :profile="profiles.get(name)!"
                  :disabled="saving"
                  :errors="fieldErrors(name)"
                />
              </div>
            </li>
          </ol>
          <section v-if="editedOutside.length" class="modern-client-catalog-outside">
            <p>{{ t('modelManager.clientCatalog.editedOutside') }}</p>
            <div v-for="name in editedOutside" :key="name" :data-model="name" tabindex="-1">
              <div class="modern-client-catalog-row-heading">
                <AppOverflowText :text="name" /><AppButton
                  variant="text"
                  size="xxs"
                  :disabled="saving"
                  @click="expanded = expanded === name ? '' : name"
                  >{{ t('modelManager.clientCatalog.attributes') }}</AppButton
                ><AppButton
                  variant="text"
                  size="xxs"
                  :disabled="saving"
                  @click="resetProfile(name)"
                  >{{ t('modelManager.profile.resetAll') }}</AppButton
                >
              </div>
              <ModelProfileForm
                v-if="expanded === name"
                v-model="drafts[name]!"
                :profile="profiles.get(name)!"
                :disabled="saving"
                :errors="fieldErrors(name)"
              />
            </div>
          </section>
        </div>
        <footer class="modern-client-catalog-footer">
          <AppButton
            variant="text"
            size="sm"
            :icon="RotateCcw"
            :disabled="saving"
            @click="resetDirectory"
            >{{ t('modelManager.clientCatalog.restore') }}</AppButton
          >
          <div class="modern-client-catalog-footer-actions">
            <AppButton size="sm" :disabled="saving" @click="close">{{ t('ui.cancel') }}</AppButton
            ><AppButton
              type="submit"
              variant="primary"
              size="sm"
              :disabled="saving || !dirty"
              :loading="saving"
              >{{ t('modelManager.clientCatalog.save') }}</AppButton
            >
          </div>
        </footer>
      </form>
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
.modern-client-catalog-summary {
  flex: none;
  display: grid;
  gap: var(--modern-space-2);
  padding: var(--modern-space-3) var(--modern-space-5);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-client-catalog-summary-line {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-client-catalog-summary p,
.modern-client-catalog-rule,
.modern-client-catalog-properties-heading {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-client-catalog-scroll {
  overflow-y: auto;
  overscroll-behavior: contain;
  min-height: 0;
  padding: var(--modern-space-4) var(--modern-space-5);
}
.modern-client-catalog-add {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--modern-space-3);
}
.modern-client-catalog-rule {
  margin-block: var(--modern-space-3);
}
.modern-client-catalog-list {
  padding: 0;
  margin: 0;
  list-style: none;
}
.modern-client-catalog-row {
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-client-catalog-row-heading {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  padding-block: var(--modern-space-3);
}
.modern-client-catalog-row-name {
  display: grid;
  flex: 1;
  gap: var(--modern-space-1);
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
}
.modern-client-catalog-row-name small,
.modern-client-catalog-position {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-client-catalog-position {
  min-width: var(--modern-space-5);
  text-align: center;
  font-variant-numeric: tabular-nums;
}
.modern-client-catalog-row-actions,
.modern-client-catalog-footer-actions {
  display: flex;
  gap: var(--modern-space-1);
  align-items: center;
}
.modern-client-catalog-grip {
  cursor: grab;
}
.modern-client-catalog-row.is-dragging {
  background: var(--modern-subtle);
}
.modern-client-catalog-row.is-drop-target {
  outline: var(--modern-line-width) solid var(--modern-accent);
}
.modern-client-catalog-row.is-overflow .modern-client-catalog-row-name {
  color: var(--modern-muted);
}
.modern-client-catalog-row.has-errors {
  border-color: var(--modern-danger);
}
.modern-client-catalog-cutoff {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-4);
  color: var(--modern-warning);
  font-size: var(--modern-font-size-small);
}
.modern-client-catalog-cutoff::before,
.modern-client-catalog-cutoff::after {
  content: '';
  flex: 1;
  border-top: var(--modern-line-width) dashed var(--modern-warning);
}
.modern-client-catalog-properties {
  display: grid;
  gap: var(--modern-space-4);
  padding: var(--modern-space-2) var(--modern-space-3) var(--modern-space-5);
}
.modern-client-catalog-properties-heading {
  display: flex;
  justify-content: space-between;
  gap: var(--modern-space-3);
  align-items: center;
}
.modern-client-catalog-outside {
  display: grid;
  gap: var(--modern-space-3);
  margin-top: var(--modern-space-5);
  font-size: var(--modern-font-size-secondary);
  color: var(--modern-muted);
}
.modern-client-catalog-outside .modern-client-catalog-row-heading > :first-child {
  flex: 1;
  min-width: 0;
}
.modern-client-catalog-footer {
  display: flex;
  flex: none;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-4) var(--modern-space-5);
}
@media (max-width: 760px) {
  .modern-client-catalog-row-heading {
    flex-wrap: wrap;
  }
  .modern-client-catalog-row-name {
    flex-basis: calc(100% - calc(var(--modern-space-8) * 2));
  }
  .modern-client-catalog-row-actions {
    margin-left: auto;
  }
  .modern-client-catalog-summary,
  .modern-client-catalog-scroll {
    padding-inline: var(--modern-space-3);
  }
  .modern-client-catalog-add {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
