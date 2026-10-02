<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getGroupPolicy,
  groupPolicyKey,
  saveGroupPolicy,
  type GroupPolicy,
} from '@modern/api/group-detail'
import type { GroupRow } from '@modern/api/groups'
import {
  AppButton,
  AppCollectionState,
  AppFormSection,
  AppNotice,
  AppTextArea,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import GroupWorkspacePanel from './GroupWorkspacePanel.vue'

const props = defineProps<{ group: GroupRow }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const controller = new AbortController()
onScopeDispose(() => controller.abort())

const query = useQuery({
  queryKey: groupPolicyKey(props.group.id),
  queryFn: ({ signal }) => getGroupPolicy(client, props.group.id, signal),
})

const currentPolicy = ref<GroupPolicy>()
const baseline = ref('')
const draft = ref('')
const saving = ref(false)
const serverError = ref('')

const dirty = computed(() => draft.value !== baseline.value)

watch(
  query.data,
  (data) => {
    if (!data || dirty.value || saving.value) return
    currentPolicy.value = data
    const formatted = JSON.stringify(data.config, null, 2)
    baseline.value = formatted
    draft.value = formatted
    serverError.value = ''
  },
  { immediate: true },
)

const isUnconfigured = computed(() => currentPolicy.value?.revision === 0)

const jsonValidation = computed<{ valid: boolean; value?: unknown; error?: string }>(() => {
  const trimmed = draft.value.trim()
  if (!trimmed) {
    return { valid: false, error: t('groupDetail.policy.invalidJson') }
  }
  try {
    const parsed = JSON.parse(trimmed)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return { valid: false, error: t('groupDetail.policy.invalidJson') }
    }
    return { valid: true, value: parsed }
  } catch {
    return { valid: false, error: t('groupDetail.policy.invalidJson') }
  }
})

const saveDisabled = computed(
  () => !dirty.value || !jsonValidation.value.valid || saving.value || !currentPolicy.value,
)

async function save(): Promise<void> {
  if (!currentPolicy.value || !dirty.value || !jsonValidation.value.valid || saving.value) return
  saving.value = true
  serverError.value = ''
  const targetConfig = jsonValidation.value.value
  const expectedRevision = currentPolicy.value.revision

  try {
    await cache.cancelQueries({ queryKey: groupPolicyKey(props.group.id) })
    const result = await saveGroupPolicy(
      client,
      props.group.id,
      expectedRevision,
      targetConfig,
      controller.signal,
    )
    if (controller.signal.aborted) return
    currentPolicy.value = result
    const formatted = JSON.stringify(result.config, null, 2)
    baseline.value = formatted
    draft.value = formatted
    cache.setQueryData(groupPolicyKey(props.group.id), result)
    emit('saved')
    emit('close')
  } catch (err: unknown) {
    if (controller.signal.aborted) return
    if (
      err instanceof ApiError &&
      (err.status === 409 ||
        err.code === 'POLICY_REVISION_CONFLICT' ||
        err.code === 'POLICY_REVISION_OVERFLOW')
    ) {
      serverError.value = t('groupDetail.policy.conflict')
    } else if (err instanceof ApiError) {
      serverError.value = err.data
        ? String(err.data)
        : err.message || t('groupDetail.policy.saveFailed')
    } else {
      serverError.value = t('groupDetail.policy.saveFailed')
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <GroupWorkspacePanel
    :title="t('groupDetail.policy.title')"
    :description="t('groupDetail.policy.description')"
    :dirty="dirty"
    :pending="saving"
    :loading="query.isPending.value"
    :save-disabled="saveDisabled"
    :save-label="t('groupDetail.policy.saveLabel')"
    wide
    @close="emit('close')"
    @save="save"
  >
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState
      v-else-if="query.isError.value && !currentPolicy"
      :title="t('groupDetail.policy.loadFailed')"
      error
    >
      <AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <template v-else>
      <AppNotice v-if="serverError" tone="danger">
        {{ serverError }}
      </AppNotice>
      <AppNotice v-else-if="isUnconfigured" tone="info">
        {{ t('groupDetail.policy.emptyState') }}
      </AppNotice>
      <AppFormSection compact :title="t('groupDetail.policy.rawJson')">
        <AppTextArea
          v-model="draft"
          :label="t('groupDetail.policy.rawJson')"
          :hint="t('groupDetail.policy.rawJsonHint')"
          :error="dirty && !jsonValidation.valid ? jsonValidation.error : undefined"
          :placeholder="t('groupDetail.policy.placeholder')"
          :disabled="saving"
          mono
          :rows="16"
        />
      </AppFormSection>
    </template>
  </GroupWorkspacePanel>
</template>
