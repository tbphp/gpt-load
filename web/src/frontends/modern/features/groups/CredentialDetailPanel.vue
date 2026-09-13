<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  credentialDetailKey,
  getCredentialDetail,
  updateCredential,
} from '@modern/api/credential-actions'
import type { CredentialRow } from '@modern/api/group-detail'
import type { GroupChannel } from '@modern/api/group-create'
import type { GroupRow } from '@modern/api/groups'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppNotice,
  AppOverflowText,
  AppSegmentedField,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import { credentialStatus, credentialTime } from './credential-presentation'
import { validProxyURL } from './group-create-rules'
import GroupWorkspacePanel from './GroupWorkspacePanel.vue'
import CredentialAccountInfo from './CredentialAccountInfo.vue'
import CredentialWindowUsage from './CredentialWindowUsage.vue'

const props = defineProps<{ group: GroupRow; row: CredentialRow; channel?: GroupChannel }>()
const emit = defineEmits<{ close: []; saved: [row: CredentialRow] }>()
const { t, n, locale, te } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const query = useQuery({
  queryKey: credentialDetailKey(props.group.id, props.row.id),
  queryFn: ({ signal }) => getCredentialDetail(client, props.group.id, props.row.id, signal),
})
const item = computed(() => query.data.value ?? props.row)
const state = computed(() => credentialStatus(item.value))
const saved = ref<CredentialRow>()
const weight = ref('')
const proxyMode = ref('inherit')
const proxyURL = ref('')
const saving = ref(false)
const attempted = ref(false)
const error = ref('')
const controller = new AbortController()
const dirty = computed(
  () =>
    Boolean(saved.value) &&
    (weight.value !== String(saved.value!.weightManual ?? '') ||
      proxyMode.value !== saved.value!.proxy.mode ||
      Boolean(proxyURL.value)),
)
watch(
  query.data,
  (value) => {
    if (!value || dirty.value || saving.value) return
    saved.value = value
    weight.value = String(value.weightManual ?? '')
    proxyMode.value = value.proxy.mode
    proxyURL.value = ''
  },
  { immediate: true },
)
const weightInvalid = computed(
  () =>
    Boolean(weight.value) &&
    (!/^\d+$/u.test(weight.value) ||
      (Number(weight.value) < 1 && Number(weight.value) !== saved.value?.weight) ||
      Number(weight.value) > 100),
)
const proxyChanged = computed(
  () => proxyMode.value !== saved.value?.proxy.mode || Boolean(proxyURL.value),
)
const proxyInvalid = computed(
  () => proxyChanged.value && proxyMode.value === 'custom' && !validProxyURL(proxyURL.value.trim()),
)
const proxyOptions = computed(() => [
  { value: 'inherit', label: t('credentialCards.proxyMode.inherit') },
  { value: 'direct', label: t('groupCreate.proxyDirect') },
  { value: 'custom', label: t('groupCreate.proxyCustom') },
])
function failure(): string {
  if (!item.value.failures) return '—'
  const key = 'credentialCards.failures.' + item.value.failureCategory
  return [te(key) ? t(key) : item.value.failureCategory, item.value.lastStatusCode]
    .filter(Boolean)
    .join(' · ')
}
async function save(): Promise<void> {
  if (!saved.value || !dirty.value || saving.value) return
  attempted.value = true
  if (weightInvalid.value || proxyInvalid.value) return
  const patch: Parameters<typeof updateCredential>[3] = {}
  if (weight.value !== String(saved.value.weightManual ?? ''))
    patch.weight_manual = weight.value ? Number(weight.value) : null
  if (proxyChanged.value)
    patch.proxy =
      proxyMode.value === 'inherit'
        ? null
        : proxyMode.value === 'direct'
          ? { mode: 'direct' }
          : { mode: 'custom', url: proxyURL.value.trim() }
  saving.value = true
  error.value = ''
  try {
    await cache.cancelQueries({ queryKey: credentialDetailKey(props.group.id, props.row.id) })
    const result = await updateCredential(
      client,
      props.group.id,
      props.row.id,
      patch,
      controller.signal,
    )
    if (controller.signal.aborted) return
    emit('saved', result)
    emit('close')
  } catch {
    if (!controller.signal.aborted) error.value = t('groups.edit.saveFailed')
  } finally {
    saving.value = false
  }
}
onScopeDispose(() => controller.abort())
</script>
<template>
  <GroupWorkspacePanel
    :title="t('groupDetail.credentialDetails')"
    :description="row.account || row.mask"
    :wide="false"
    :dirty="dirty"
    :pending="saving"
    :loading="query.isFetching.value"
    :save-disabled="!saved"
    @close="emit('close')"
    @save="save"
  >
    <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState
      v-else-if="query.isError.value && !saved"
      :title="t('groups.edit.loadFailed')"
      error
      ><AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton></AppCollectionState
    >
    <template v-else>
      <CredentialWindowUsage
        v-if="item.observation?.windows.length"
        :windows="item.observation.windows"
      />
      <AppNotice v-if="query.isError.value" tone="warning">{{
        t('groupDetail.refreshFailed')
      }}</AppNotice>
      <section class="modern-credential-detail-section">
        <div class="modern-credential-detail-title">
          <h3>{{ t('credentialCards.runtime') }}</h3>
          <AppBadge :tone="state.tone" dot>{{ t(state.key) }}</AppBadge>
        </div>
        <dl class="modern-credential-detail-metrics">
          <div>
            <dt>{{ t('groupDetail.recentSuccess') }}</dt>
            <dd>{{ n(item.successes) }}</dd>
          </div>
          <div>
            <dt>{{ t('groupDetail.recentFailure') }}</dt>
            <dd>{{ n(item.failures) }}</dd>
          </div>
          <div>
            <dt>{{ t('groupDetail.failuresInRow') }}</dt>
            <dd>{{ n(item.failuresInRow) }}</dd>
          </div>
          <div>
            <dt>{{ t('groupDetail.lastUsed') }}</dt>
            <dd>{{ credentialTime(item.lastUsed, locale) }}</dd>
          </div>
          <div>
            <dt>{{ t('credentialCards.lastFailure') }}</dt>
            <dd>{{ failure() }}</dd>
          </div>
          <div>
            <dt>{{ t('credentialCards.recovery') }}</dt>
            <dd>
              {{ t('credentialCards.recoveryModes.' + item.recovery.mode)
              }}<span v-if="item.recovery.at">
                · {{ credentialTime(item.recovery.at, locale) }}</span
              >
            </dd>
          </div>
        </dl>
        <p v-if="item.daily && !item.daily.complete" class="modern-credential-detail-hint">
          {{ t('groups.row.partialHelp') }}
        </p>
      </section>
      <section
        v-if="group.connectionType === 'subscription'"
        class="modern-credential-detail-section"
      >
        <h3>{{ t('credentialCards.accountInfo') }}</h3>
        <CredentialAccountInfo :row="item" />
      </section>
      <section v-if="item.modelCooldowns.length" class="modern-credential-detail-section">
        <h3>{{ t('credentialCards.cooldownModels') }}</h3>
        <div
          v-for="cooldown in item.modelCooldowns"
          :key="cooldown.model"
          class="modern-credential-detail-title"
        >
          <AppOverflowText :text="cooldown.model" /><small>{{
            credentialTime(cooldown.until, locale)
          }}</small>
        </div>
      </section>
      <section class="modern-credential-detail-section">
        <h3>{{ t('credentialCards.settings') }}</h3>
        <AppTextField
          v-model="weight"
          :label="t('groups.edit.weight')"
          :description="t('credentialCards.autoWeight')"
          :placeholder="t('credentialCards.automatic')"
          inputmode="numeric"
          :disabled="saving"
          :error="attempted && weightInvalid ? t('groups.edit.weightError') : undefined"
        />
        <template v-if="channel?.proxy"
          ><AppSegmentedField
            v-model="proxyMode"
            :label="t('groupCreate.proxy')"
            :options="proxyOptions"
            :disabled="saving" /><AppTextField
            v-if="proxyMode === 'custom'"
            v-model="proxyURL"
            :label="t('groupCreate.proxyURL')"
            :placeholder="saved?.proxy.mode === 'custom' ? saved.proxy.display : undefined"
            :description="
              saved?.proxy.mode === 'custom' ? t('groupDetail.proxyUnchanged') : undefined
            "
            :disabled="saving"
            :error="attempted && proxyInvalid ? t('groupCreate.proxyError') : undefined"
            autocomplete="off"
        /></template>
      </section>
    </template>
  </GroupWorkspacePanel>
</template>
<style scoped>
.modern-credential-detail-section {
  display: grid;
  gap: var(--modern-space-4);
}
.modern-window-usage + .modern-credential-detail-section,
.modern-credential-detail-section + .modern-credential-detail-section {
  padding-top: var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-credential-detail-section h3 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-credential-detail-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--modern-space-3);
  min-width: 0;
  font-size: var(--modern-font-size-secondary);
}
.modern-credential-detail-title small,
.modern-credential-detail-hint {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-credential-detail-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-4);
}
.modern-credential-detail-metrics dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  margin-bottom: var(--modern-space-1);
}
.modern-credential-detail-metrics dd {
  margin: 0;
  font-size: var(--modern-font-size-secondary);
  overflow-wrap: anywhere;
}
</style>
