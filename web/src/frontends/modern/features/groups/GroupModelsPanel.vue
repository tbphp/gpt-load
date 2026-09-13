<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useURLState } from '@modern/app/url-state'
import { useMessageSource } from '@modern/app/messages'
import { useI18n } from 'vue-i18n'
import {
  discoverGroupModels,
  getGroupModels,
  groupModelsKey,
  saveGroupModels,
} from '@modern/api/group-detail'
import type { GroupChannel } from '@modern/api/group-create'
import type { GroupRow } from '@modern/api/groups'
import type { ModelCandidate } from '@modern/api/model-discovery'
import { AppButton, AppCollectionState } from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import { modelErrors, type GroupDraftModel } from './group-create-rules'
import GroupModelPicker from './GroupModelPicker.vue'
import GroupWorkspacePanel from './GroupWorkspacePanel.vue'
import GroupModelSyncDialog from './GroupModelSyncDialog.vue'

const props = defineProps<{ group: GroupRow; channel?: GroupChannel }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const query = useQuery({
  queryKey: groupModelsKey(props.group.id),
  queryFn: ({ signal }) => getGroupModels(client, props.group.id, signal),
})
const draft = ref<GroupDraftModel[]>([])
const baseline = ref('')
const initialized = ref(false)
const saving = ref(false)
const syncView = useURLState(
  ['sync_models'],
  (query) => ({ open: query.sync_models === '1' }),
  (value) => (value.open ? { sync_models: '1' } : {}),
)
const syncing = computed({
  get: () => syncView.value.open,
  set: (open: boolean) => {
    syncView.value = { open }
  },
})
const attempted = ref(false)
const error = ref('')
const candidates = ref<ModelCandidate[]>([])
const discovering = ref(false)
const discoveryError = ref('')
const picker = ref<InstanceType<typeof GroupModelPicker>>()
const controller = new AbortController()
let discovery: AbortController | undefined
const signature = () => JSON.stringify(draft.value.map(({ id, alias }) => ({ id, alias })))
const dirty = computed(() => initialized.value && signature() !== baseline.value)
watch(
  query.data,
  (models) => {
    if (!models || dirty.value || saving.value) return
    draft.value = models.map((model, key) => ({
      key,
      id: model.id,
      alias: model.alias,
      origin: 'configured',
    }))
    baseline.value = signature()
    initialized.value = true
  },
  { immediate: true },
)
const prices = computed(
  () =>
    new Map(
      (query.data.value ?? []).map((model) => [
        model.id,
        { pricingStatus: model.pricingStatus, pricingSource: null },
      ]),
    ),
)
function cancelDiscovery(): void {
  discovery?.abort()
  discovering.value = false
}
async function discover(): Promise<void> {
  cancelDiscovery()
  const request = new AbortController()
  discovery = request
  discovering.value = true
  discoveryError.value = ''
  try {
    candidates.value = await discoverGroupModels(client, props.group.id, request.signal)
  } catch {
    if (!request.signal.aborted) discoveryError.value = t('groupCreate.discoveryFailed')
  } finally {
    if (!request.signal.aborted) discovering.value = false
  }
}
async function save(): Promise<void> {
  if (!dirty.value || saving.value) return
  attempted.value = true
  if (modelErrors(draft.value).size) {
    await picker.value?.focusFirstInvalid()
    return
  }
  saving.value = true
  error.value = ''
  cancelDiscovery()
  try {
    await cache.cancelQueries({ queryKey: groupModelsKey(props.group.id) })
    const result = await saveGroupModels(client, props.group.id, draft.value, controller.signal)
    if (controller.signal.aborted) return
    baseline.value = signature()
    cache.setQueryData(groupModelsKey(props.group.id), result)
    void cache.invalidateQueries({ queryKey: ['modern', 'group-model-names', props.group.id] })
    emit('saved')
    emit('close')
  } catch {
    if (!controller.signal.aborted) error.value = t('groups.edit.saveFailed')
  } finally {
    saving.value = false
  }
}
async function syncModels(models: GroupDraftModel[]): Promise<void> {
  if (saving.value || dirty.value) return
  draft.value = models
  syncing.value = false
  await save()
}
onScopeDispose(() => {
  controller.abort()
  cancelDiscovery()
})
useMessageSource(() => (error.value ? { text: error.value, tone: 'danger' } : undefined))
</script>

<template>
  <GroupWorkspacePanel
    :title="t('groupDetail.modelsAndAliases')"
    :description="group.name"
    :dirty="dirty"
    :pending="saving"
    :loading="query.isFetching.value"
    :save-disabled="!initialized"
    wide
    fill
    @close="emit('close')"
    @save="save"
  >
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState v-else-if="!initialized" :title="t('groups.edit.loadFailed')" error
      ><AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton></AppCollectionState
    >
    <template v-else>
      <GroupModelPicker
        ref="picker"
        v-model="draft"
        layout="list"
        :candidates="candidates"
        :configured-pricing="prices"
        :connection-revision="group.id"
        :discovery-supported="Boolean(channel?.discovery)"
        :can-discover="Boolean(channel?.discovery)"
        :loading="discovering"
        :discovery-error="discoveryError"
        :disabled="saving"
        :attempted="attempted"
        @discover="discover"
        @cancel-discovery="cancelDiscovery"
      >
        <template #actions>
          <AppButton
            v-if="channel?.discovery"
            :icon="RefreshCw"
            size="sm"
            variant="ghost"
            :disabled="!initialized || dirty || saving || discovering"
            @click="syncing = true"
          >
            {{ t('groupWorkflows.syncModels') }}
          </AppButton>
        </template>
      </GroupModelPicker>
    </template>
  </GroupWorkspacePanel>
  <GroupModelSyncDialog
    v-if="syncing && initialized"
    :group-id="group.id"
    :models="draft"
    @close="syncing = false"
    @confirm="syncModels"
  />
</template>
