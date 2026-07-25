<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { RefreshCw, Save, TriangleAlert } from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { discoverGroupModels, replaceGroupModels, type GroupDetailDto } from '@/api/control/groups'
import { ApiError, RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import ModelDraftEditor, {
  type ModelDraftEditorItem,
} from '@/components/config/ModelDraftEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import {
  buildModelDiff,
  hasModelRemovals,
  normalizeSelectedModels,
  sameNormalizedModels,
  type ModelDiffItem,
} from './model-diff'

const props = defineProps<{ groupId: number; group: GroupDetailDto }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const savedModels = ref(props.group.models.map((model) => ({ ...model })))
const draft = ref<ModelDiffItem[]>(buildModelDiff(savedModels.value, []))
const discoveryRan = ref(false)
const pendingAction = ref<'discover' | 'save' | null>(null)
const discoveryError = ref<'none' | 'no-active-key' | 'bad-gateway' | 'generic'>('none')
const saveError = ref(false)
const emptyConfirmOpen = ref(false)
let activeController: AbortController | undefined

const normalizedModels = computed(() => normalizeSelectedModels(draft.value))
const emptySelection = computed(() => normalizedModels.value.length === 0)
const changed = computed(() => !sameNormalizedModels(savedModels.value, draft.value))
const removals = computed(() => hasModelRemovals(savedModels.value, draft.value))
const pending = computed(() => pendingAction.value !== null)

watch(
  () => props.group.models,
  (models) => {
    if (pending.value || changed.value || discoveryRan.value) return
    savedModels.value = models.map((model) => ({ ...model }))
    draft.value = buildModelDiff(savedModels.value, [])
  },
  { deep: true },
)

function startAction(action: 'discover' | 'save'): AbortController {
  activeController?.abort()
  activeController = new AbortController()
  pendingAction.value = action
  return activeController
}

function finishAction(controller: AbortController): void {
  if (activeController !== controller) return
  activeController = undefined
  pendingAction.value = null
}

function updateDraft(value: ModelDraftEditorItem[]): void {
  draft.value = value.map((model, index) => {
    const existing = draft.value[index]
    return {
      id: model.id,
      alias: model.alias,
      selected: model.selected,
      origin: existing?.origin ?? 'manual',
      rediscovered: existing?.rediscovered ?? false,
    }
  })
}

function statusLabel(index: number): string {
  const model = draft.value[index]
  if (!model) return ''
  if (model.origin === 'manual') return t('group.modelEditor.status.manual')
  if (model.origin === 'discovered') return t('group.modelEditor.status.discovered')
  if (!discoveryRan.value) return t('group.modelEditor.status.saved')
  return model.rediscovered
    ? t('group.modelEditor.status.rediscovered')
    : t('group.modelEditor.status.notRediscovered')
}

function statusTone(index: number): 'success' | 'warning' | 'neutral' {
  const model = draft.value[index]
  if (model?.origin === 'discovered' || (discoveryRan.value && model?.rediscovered)) {
    return 'success'
  }
  if (discoveryRan.value && model?.origin === 'persisted' && !model.rediscovered) {
    return 'warning'
  }
  return 'neutral'
}

async function runDiscovery(): Promise<void> {
  if (pending.value) return
  discoveryError.value = 'none'
  saveError.value = false
  const controller = startAction('discover')
  try {
    const result = await discoverGroupModels(client, props.groupId, controller.signal)
    if (activeController !== controller) return
    const discoveredIDs = new Set(result.models.map((id) => id.trim()).filter(Boolean))
    const representedIDs = new Set(draft.value.map((model) => model.id.trim()))
    draft.value = [
      ...draft.value.map((model) => ({
        ...model,
        rediscovered:
          model.origin === 'persisted' ? discoveredIDs.has(model.id.trim()) : model.rediscovered,
      })),
      ...Array.from(discoveredIDs)
        .filter((id) => !representedIDs.has(id))
        .map<ModelDiffItem>((id) => ({
          id,
          alias: '',
          selected: true,
          origin: 'discovered',
          rediscovered: true,
        })),
    ]
    discoveryRan.value = true
  } catch (error: unknown) {
    if (activeController !== controller || error instanceof RequestCancelledError) return
    if (error instanceof ApiError && error.code === 'NO_ACTIVE_UPSTREAM_KEY') {
      discoveryError.value = 'no-active-key'
    } else if (error instanceof ApiError && error.code === 'BAD_GATEWAY') {
      discoveryError.value = 'bad-gateway'
    } else {
      discoveryError.value = 'generic'
    }
  } finally {
    finishAction(controller)
  }
}

function requestSave(): void {
  if (pending.value || !changed.value) return
  saveError.value = false
  if (emptySelection.value) {
    emptyConfirmOpen.value = true
    return
  }
  void runReplace()
}

async function confirmEmptyReplace(): Promise<void> {
  emptyConfirmOpen.value = false
  await nextTick()
  await runReplace()
}

async function runReplace(): Promise<void> {
  if (pending.value || !changed.value) return
  const models = normalizeSelectedModels(draft.value)
  const controller = startAction('save')
  saveError.value = false
  try {
    const result = await replaceGroupModels(client, props.groupId, { models }, controller.signal)
    if (activeController !== controller) return
    await queryClient.cancelQueries({
      queryKey: controlQueryKeys.groups.detail(props.groupId),
      exact: true,
    })
    if (activeController !== controller) return
    savedModels.value = result.models.map((model) => ({ ...model }))
    draft.value = buildModelDiff(savedModels.value, [])
    discoveryRan.value = false
    discoveryError.value = 'none'
    queryClient.setQueryData(controlQueryKeys.groups.detail(props.groupId), result)
    await queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.list() })
  } catch (error: unknown) {
    if (activeController === controller && !(error instanceof RequestCancelledError))
      saveError.value = true
  } finally {
    finishAction(controller)
  }
}

onBeforeUnmount(() => {
  activeController?.abort()
  activeController = undefined
})
</script>

<template>
  <section class="group-models" aria-labelledby="group-models-heading">
    <header class="group-models__header">
      <div>
        <h2 id="group-models-heading">{{ t('group.modelEditor.title') }}</h2>
        <p>{{ t('group.modelEditor.description') }}</p>
      </div>
      <div class="group-models__actions">
        <AppButton
          data-test="models-discover"
          variant="secondary"
          :busy="pendingAction === 'discover'"
          :disabled="pending"
          @click="runDiscovery"
        >
          <RefreshCw :size="16" aria-hidden="true" />{{ t('group.modelEditor.rediscover') }}
        </AppButton>
        <AppDialog
          v-if="emptySelection"
          :open="emptyConfirmOpen"
          :title="t('group.modelEditor.emptyConfirm.title')"
          :description="t('group.modelEditor.emptyConfirm.description')"
          :close-label="t('group.modelEditor.emptyConfirm.close')"
          @update:open="emptyConfirmOpen = $event"
        >
          <template #trigger>
            <AppButton
              data-test="models-save"
              :busy="pendingAction === 'save'"
              :disabled="pending || !changed"
              @click="requestSave"
            >
              <Save :size="16" aria-hidden="true" />{{ t('group.modelEditor.save') }}
            </AppButton>
          </template>
          <div class="group-models__dialog-actions">
            <AppButton variant="secondary" @click="emptyConfirmOpen = false">
              {{ t('group.modelEditor.emptyConfirm.cancel') }}
            </AppButton>
            <AppButton
              class="group-models__confirm-empty"
              data-test="models-empty-confirm"
              variant="secondary"
              @click="confirmEmptyReplace"
            >
              {{ t('group.modelEditor.emptyConfirm.confirm') }}
            </AppButton>
          </div>
        </AppDialog>
        <AppButton
          v-else
          data-test="models-save"
          :busy="pendingAction === 'save'"
          :disabled="pending || !changed"
          @click="requestSave"
        >
          <Save :size="16" aria-hidden="true" />{{ t('group.modelEditor.save') }}
        </AppButton>
      </div>
    </header>

    <div v-if="discoveryError === 'no-active-key'" class="group-models__no-key" role="alert">
      <TriangleAlert :size="18" aria-hidden="true" />
      <div>
        <strong>{{ t('group.modelEditor.noActiveKey.title') }}</strong>
        <p>{{ t('group.modelEditor.noActiveKey.description') }}</p>
        <div class="group-models__guidance-actions">
          <RouterLink
            data-test="models-keys-action"
            class="button-link"
            :to="{ name: 'group-detail', params: { id: groupId }, query: { tab: 'keys' } }"
          >
            {{ t('group.modelEditor.noActiveKey.keysAction') }}
          </RouterLink>
          <RouterLink
            data-test="models-import-action"
            class="button-link button-link--secondary"
            :to="`/import?mode=existing&group_id=${groupId}`"
          >
            {{ t('group.modelEditor.noActiveKey.importAction') }}
          </RouterLink>
        </div>
      </div>
    </div>
    <InlineFeedback v-else-if="discoveryError === 'bad-gateway'" tone="warning">
      {{ t('group.modelEditor.badGateway') }}
    </InlineFeedback>
    <InlineFeedback v-else-if="discoveryError === 'generic'" tone="warning">
      {{ t('group.modelEditor.discoveryFailed') }}
    </InlineFeedback>
    <InlineFeedback v-if="saveError" tone="danger">
      {{ t('group.modelEditor.saveFailed') }}
    </InlineFeedback>
    <InlineFeedback v-if="removals" data-test="models-removal-warning" tone="warning">
      {{ t('group.modelEditor.removalWarning') }}
    </InlineFeedback>

    <ModelDraftEditor :model-value="draft" :disabled="pending" @update:model-value="updateDraft">
      <template #status="{ index }">
        <StatusBadge :data-test="`model-status-${index}`" :tone="statusTone(index)">
          {{ statusLabel(index) }}
        </StatusBadge>
      </template>
    </ModelDraftEditor>
  </section>
</template>

<style scoped>
.group-models {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}
.group-models__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
.group-models__header h2,
.group-models__header p,
.group-models__no-key p {
  margin: 0;
}
.group-models__header h2 {
  font-size: 1.125rem;
}
.group-models__header p,
.group-models__no-key p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.group-models__actions,
.group-models__guidance-actions,
.group-models__dialog-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.group-models__actions :deep(.app-button) {
  gap: var(--space-2);
}
.group-models__no-key {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: var(--space-3);
  border: 1px solid color-mix(in srgb, var(--color-warning) 38%, var(--color-border));
  border-radius: var(--radius-card);
  background: var(--color-warning-bg);
  color: var(--color-warning);
  padding: var(--space-4);
}
.group-models__guidance-actions {
  margin-top: var(--space-3);
}
.group-models__guidance-actions .button-link {
  min-height: 44px;
}
.group-models__dialog-actions {
  justify-content: flex-end;
}
.group-models__confirm-empty {
  border-color: var(--color-danger);
  color: var(--color-danger);
}
@media (max-width: 640px) {
  .group-models__header {
    align-items: stretch;
    flex-direction: column;
  }
  .group-models__actions :deep(.app-button),
  .group-models__actions :deep(.app-dialog__trigger) {
    width: 100%;
  }
}
</style>
