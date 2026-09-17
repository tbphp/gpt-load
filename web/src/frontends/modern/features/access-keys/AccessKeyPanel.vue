<script setup lang="ts">
import { protocolLabel } from '@modern/i18n/protocols'
import { Eye, EyeOff, RefreshCw, Trash2 } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { DialogRoot } from 'reka-ui'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  accessDetailKey,
  accessProtocols,
  createAccessKey,
  deleteAccessKey,
  getAccessKey,
  revealAccessKey,
  updateAccessKey,
  type AccessFilters,
  type AccessKey,
} from '@modern/api/access-keys'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { useMessageSource } from '@modern/app/messages'
import {
  AppProtocolTag,
  AppBadge,
  AppDateTimePicker,
  AppButton,
  AppChannelIcon,
  AppCollectionState,
  AppConfirmDialog,
  AppCopyValue,
  AppDialogContent,
  AppDialogHeader,
  AppFormSection,
  AppIconButton,
  AppMultiSelect,
  AppSegmentedControl,
  AppSwitch,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'
import { formatLocalDateTime } from '@modern/components/ui/date-time'
import { formatCompactNumber, formatNanoUSD } from '@modern/components/ui/format'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'
import { createOperationKey } from '../groups/group-create-operation'
import AccessKeyQuotaEditor from './AccessKeyQuotaEditor.vue'
import AccessKeyQuotaResetDialog from './AccessKeyQuotaResetDialog.vue'
import { accessState, accessTime } from './access-key-display'
import {
  draftErrors,
  draftFor,
  generateKey,
  inputFor,
  keyStrength,
  patchFor,
  type AccessDraft,
} from './access-key-draft'

const props = defineProps<{
  mode: 'create' | 'detail' | 'copy'
  id?: number
  hint: AccessFilters
}>()
const emit = defineEmits<{ close: []; saved: [row: AccessKey, created: boolean]; deleted: [] }>()
const { t, n, locale } = useI18n()
const client = useApiClient()
const controller = new AbortController()
const query = useQuery({
  queryKey: accessDetailKey(props.id ?? 0),
  queryFn: ({ signal }) => getAccessKey(client, props.id!, signal, props.hint),
  enabled: Boolean(props.id),
})
const groups = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
})
const base = ref<AccessKey>()
const usageRow = computed(() => (props.mode === 'detail' ? query.data.value : undefined))
const draft = ref<AccessDraft>(draftFor())
const initial = ref('')
const initialized = ref(false)
const attempted = ref(false)
const pending = ref(false)
const completed = ref(false)
const error = ref('')
const form = ref<HTMLFormElement>()
const guard = ref<InstanceType<typeof AppDraftGuard>>()
const secret = ref('')
const showKey = ref(false)
const revealing = ref(false)
const deleteOpen = ref(false)
const resetOpen = ref(false)
let submission: { payload: string; operation: string } | undefined
const dirty = computed(
  () =>
    initialized.value &&
    !completed.value &&
    (props.mode === 'copy' || JSON.stringify(draft.value) !== initial.value),
)
const errors = computed(() => draftErrors(draft.value, base.value))
const fieldError = (key: string) =>
  attempted.value && errors.value[key] ? t('accessKeys.' + errors.value[key]) : undefined
const scopeOptions = computed(() => [
  { value: 'all', label: t('accessKeys.unrestricted') },
  { value: 'specified', label: t('accessKeys.specified') },
])
const expirationOptions = computed(() => [
  { value: 'never', label: t('accessKeys.never') },
  { value: 'specified', label: t('accessKeys.fixedExpiry') },
])
const expiryShortcuts = computed(() =>
  [1, 7, 30, 90].map((days) => ({
    label: t('ui.date.afterDays', { count: n(days) }),
    resolve: () => {
      const date = new Date()
      date.setDate(date.getDate() + days)
      date.setMilliseconds(0)
      return formatLocalDateTime(date)
    },
  })),
)
const expiryMode = computed({
  get: () => (draft.value.never ? 'never' : 'specified'),
  set: (value: string) => {
    draft.value.never = value === 'never'
  },
})
const groupMap = computed(
  () => new Map(groups.data.value?.items.map((group) => [String(group.id), group])),
)
const groupOptions = computed(() => [
  ...(groups.data.value?.items ?? []).map((group) => ({
    value: String(group.id),
    label: group.name,
    keywords: [group.channelName, group.channelID],
  })),
  ...draft.value.scope.groups
    .filter((id) => !groupMap.value.has(String(id)))
    .map((id) => ({ value: String(id), label: t('accessKeys.missingGroup') })),
])
const selectedGroups = computed({
  get: () => draft.value.scope.groups.map(String),
  set: (values: string[]) => {
    draft.value.scope.groups = values.map(Number)
  },
})
const protocolOptions = computed(() =>
  [...new Set([...accessProtocols, ...(base.value?.filters.protocols ?? [])])].map((value) => ({
    value,
    label: protocolLabel(value, t),
  })),
)
const modelOptions = computed(() =>
  [
    ...new Set(
      (groups.data.value?.items ?? [])
        .filter(
          (group) =>
            draft.value.modes.groups === 'all' || draft.value.scope.groups.includes(group.id),
        )
        .flatMap((group) => group.modelNames),
    ),
  ]
    .sort()
    .map((value) => ({ value, label: value })),
)
const modelMismatch = computed(
  () =>
    draft.value.modes.models !== 'all' &&
    groups.data.value &&
    draft.value.scope.models.some(
      (model) => !modelOptions.value.some((option) => option.value === model),
    ),
)
const strength = computed(() => keyStrength(draft.value.key))
const scopeLocked = computed(() => pending.value || groups.isPending.value || groups.isError.value)
function restore(value?: AccessKey): void {
  draft.value = draftFor(value, props.mode === 'copy')
  if (props.mode === 'copy' && value)
    draft.value.name = t('accessKeys.copyName', { name: value.name })
  initial.value = JSON.stringify(draft.value)
  initialized.value = true
  attempted.value = false
  secret.value = ''
  showKey.value = false
}
if (!props.id) restore()
watch(
  query.data,
  (row) => {
    if (!row || dirty.value || pending.value || completed.value) return
    base.value = props.mode === 'detail' ? row : undefined
    restore(row)
  },
  { immediate: true },
)
function revert(): void {
  draft.value = JSON.parse(initial.value) as AccessDraft
  attempted.value = false
  secret.value = ''
  showKey.value = false
  error.value = ''
}
async function close(): Promise<void> {
  if (pending.value) return
  if (await guard.value?.confirm()) emit('close')
}
function resolveSecret(): Promise<string> {
  return revealAccessKey(client, base.value!.id, controller.signal)
}
async function toggleSecret(): Promise<void> {
  if (revealing.value) return
  if (secret.value) {
    secret.value = ''
    return
  }
  if (!base.value) return
  revealing.value = true
  try {
    const value = await revealAccessKey(client, base.value.id, controller.signal)
    if (!controller.signal.aborted) secret.value = value
  } catch {
    if (!controller.signal.aborted) error.value = t('accessKeys.failed')
  } finally {
    revealing.value = false
  }
}
function generate(): void {
  try {
    draft.value.key = generateKey()
    showKey.value = true
  } catch {
    error.value = t('accessKeys.failed')
  }
}
async function requestSave(): Promise<void> {
  if (pending.value || revealing.value) return
  if (!dirty.value) return
  attempted.value = true
  if (Object.keys(errors.value).length) {
    await nextTick()
    form.value?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus()
    return
  }
  const input = inputFor(draft.value)
  if (
    (groups.isPending.value || groups.isError.value) &&
    (!base.value || JSON.stringify(input.filters) !== JSON.stringify(base.value.filters))
  ) {
    error.value = t('accessKeys.catalogFailed')
    return
  }
  if (
    input.filters.groups.some(
      (id) => !groupMap.value.has(String(id)) && !base.value?.filters.groups.includes(id),
    )
  ) {
    error.value = t('accessKeys.catalogFailed')
    return
  }
  const current = base.value
  const patch = current ? patchFor(current, input) : undefined
  pending.value = true
  error.value = ''
  let saved: AccessKey | undefined
  try {
    if (!current || patch?.key) {
      const payload = JSON.stringify(patch ?? input)
      // 同一内容再次保存时复用幂等键，避免网络异常后重复创建或替换。
      if (submission?.payload !== payload) submission = { payload, operation: createOperationKey() }
    }
    saved = current
      ? await updateAccessKey(
          client,
          current.id,
          patch!,
          controller.signal,
          patch?.key ? submission!.operation : undefined,
        )
      : await createAccessKey(client, input, submission!.operation, controller.signal)
    if (controller.signal.aborted) return
    submission = undefined
    completed.value = true
    secret.value = ''
    draft.value.key = ''
  } catch (cause) {
    if (controller.signal.aborted) return
    const code = cause instanceof ApiError ? cause.code : ''
    error.value = t(
      code === 'DUPLICATE_RESOURCE'
        ? 'accessKeys.duplicate'
        : code === 'ACCESS_KEY_ADMIN_CONFLICT'
          ? 'accessKeys.adminConflict'
          : code === 'INVALID_CUSTOM_ACCESS_KEY'
            ? 'accessKeys.invalidKey'
            : 'accessKeys.failed',
    )
  } finally {
    pending.value = false
  }
  if (saved && !controller.signal.aborted) {
    await nextTick()
    emit('saved', saved, !current)
  }
}
async function remove(): Promise<void> {
  if (!base.value || pending.value) return
  pending.value = true
  error.value = ''
  try {
    await deleteAccessKey(client, base.value.id, controller.signal)
    if (controller.signal.aborted) return
    completed.value = true
    deleteOpen.value = false
    secret.value = ''
  } catch (cause) {
    if (!controller.signal.aborted && cause instanceof ApiError && cause.status === 404) {
      completed.value = true
      deleteOpen.value = false
    } else if (!controller.signal.aborted) error.value = t('accessKeys.failed')
  } finally {
    pending.value = false
  }
  if (completed.value && !controller.signal.aborted) {
    await nextTick()
    emit('deleted')
  }
}
function openDelete(): void {
  error.value = ''
  deleteOpen.value = true
}
function openReset(): void {
  error.value = ''
  resetOpen.value = true
}
function quotaReset(): void {
  resetOpen.value = false
  const row = query.data.value
  if (row && !query.isError.value) {
    base.value = row
    restore(row)
  }
}
useMessageSource(() =>
  query.isError.value && initialized.value
    ? {
        text: t('accessKeys.stale'),
        tone: 'warning',
        action: { label: t('ui.retry'), run: () => query.refetch() },
      }
    : undefined,
)
useLoadingActivity(() => pending.value || revealing.value)
useMessageSource(() =>
  error.value && !deleteOpen.value && !resetOpen.value
    ? { text: error.value, tone: 'danger' }
    : undefined,
)
useMessageSource(() =>
  groups.isError.value
    ? {
        text: t('accessKeys.catalogFailed'),
        tone: 'warning',
        action: { label: t('ui.retry'), run: () => groups.refetch() },
      }
    : undefined,
)
onScopeDispose(() => {
  controller.abort()
  secret.value = ''
  draft.value.key = ''
  submission = undefined
})
</script>

<template>
  <DialogRoot
    :open="true"
    @update:open="
      (open) => {
        if (!open) close()
      }
    "
  >
    <AppDialogContent
      placement="editor"
      :title="base?.name ?? t('accessKeys.create')"
      :description="t('accessKeys.edit')"
    >
      <AppDialogHeader
        :title="base?.name ?? t('accessKeys.create')"
        :close-label="t('ui.close')"
        :close-disabled="pending"
        @close="close"
      />
      <AppCollectionState
        v-if="!initialized && query.isPending.value"
        :title="t('collection.loading')"
        loading
      />
      <AppCollectionState
        v-else-if="!initialized"
        :title="t(query.isError.value ? 'accessKeys.loadFailed' : 'accessKeys.notFound')"
        error
        ><AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton></AppCollectionState
      >
      <form v-else ref="form" class="modern-access-editor" novalidate @submit.prevent="requestSave">
        <div class="modern-access-editor-body">
          <AppFormSection v-if="usageRow" :title="t('accessKeys.usage')" compact>
            <template #actions
              ><AppBadge :tone="accessState(usageRow).tone" size="xs" dot>{{
                t('accessKeys.' + accessState(usageRow).key)
              }}</AppBadge></template
            >
            <dl class="modern-access-usage">
              <div>
                <dt>{{ t('accessKeys.cost') }}</dt>
                <dd>
                  {{
                    usageRow.usage
                      ? formatNanoUSD(
                          usageRow.usage.estimated_cost_nano_usd,
                          locale,
                          'narrowSymbol',
                        )
                      : '—'
                  }}
                </dd>
              </div>
              <div>
                <dt>{{ t('accessKeys.requests') }}</dt>
                <dd>
                  {{
                    usageRow.usage ? formatCompactNumber(usageRow.usage.request_count, locale) : '—'
                  }}
                </dd>
              </div>
              <div>
                <dt>{{ t('accessKeys.tokens') }}</dt>
                <dd>
                  {{
                    usageRow.usage ? formatCompactNumber(usageRow.usage.total_tokens, locale) : '—'
                  }}
                </dd>
              </div>
            </dl>
            <p class="modern-access-note">
              {{ t('accessKeys.lastUsed') }} · {{ accessTime(usageRow.last_request_at_ms, locale) }}
            </p>
          </AppFormSection>
          <AppFormSection :title="t('accessKeys.basic')" compact>
            <template #actions
              ><AppSwitch
                :model-value="draft.enabled"
                :label="t('accessKeys.active')"
                size="sm"
                :disabled="pending"
                @update:model-value="draft.enabled = $event"
            /></template>
            <AppTextField
              v-model="draft.name"
              :label="t('accessKeys.name')"
              size="sm"
              :disabled="pending"
              :error="fieldError('name')"
              autocomplete="off"
            />
            <div v-if="base" class="modern-access-current-key">
              <AppCopyValue
                :key="base.updated_at_ms"
                :value="secret || base.masked_key"
                :resolve-value="resolveSecret"
                :label="t('accessKeys.copy')"
              /><AppIconButton
                :icon="secret ? EyeOff : Eye"
                :label="t(secret ? 'accessKeys.hide' : 'accessKeys.show')"
                size="xs"
                :loading="revealing"
                :disabled="pending"
                @click="toggleSecret"
              />
            </div>
            <div class="modern-access-secret-heading">
              <span>{{ t(base ? 'accessKeys.replacement' : 'accessKeys.key') }}</span
              ><AppButton
                :icon="RefreshCw"
                variant="ghost"
                size="xs"
                :disabled="pending"
                @click="generate"
                >{{ t('accessKeys.generate') }}</AppButton
              >
            </div>
            <AppTextField
              v-model="draft.key"
              :label="t(base ? 'accessKeys.replacement' : 'accessKeys.key')"
              label-hidden
              :type="showKey ? 'text' : 'password'"
              :placeholder="
                t(base ? 'accessKeys.replacementPlaceholder' : 'accessKeys.keyPlaceholder')
              "
              size="sm"
              :disabled="pending"
              :error="fieldError('key')"
              autocomplete="new-password"
              spellcheck="false"
              ><template #suffix
                ><AppIconButton
                  :icon="showKey ? EyeOff : Eye"
                  :label="t(showKey ? 'accessKeys.hide' : 'accessKeys.show')"
                  size="xxs"
                  :disabled="pending"
                  @click="showKey = !showKey" /></template
            ></AppTextField>
            <AppBadge
              v-if="strength"
              :tone="
                strength === 'weak' ? 'warning' : strength === 'strong' ? 'success' : 'neutral'
              "
              variant="plain"
              size="xs"
              >{{ t('accessKeys.' + strength) }}</AppBadge
            >
            <div class="modern-access-setting">
              <span>{{ t('accessKeys.expirationMode') }}</span
              ><AppSegmentedControl
                v-model="expiryMode"
                :label="t('accessKeys.expirationMode')"
                :options="expirationOptions"
                appearance="field"
                size="sm"
                :disabled="pending"
              />
            </div>
            <AppDateTimePicker
              v-if="!draft.never"
              v-model="draft.expires"
              :label="t('accessKeys.expires')"
              label-hidden
              :shortcuts="expiryShortcuts"
              :min="formatLocalDateTime(Date.now())"
              size="sm"
              :disabled="pending"
              :error="fieldError('expires')"
            />
          </AppFormSection>
          <AppFormSection
            :title="t('accessKeys.scope')"
            :description="t('accessKeys.scopeHelp')"
            compact
          >
            <div class="modern-access-setting">
              <span>{{ t('accessKeys.groups') }}</span
              ><AppSegmentedControl
                v-model="draft.modes.groups"
                :label="t('accessKeys.groups')"
                :options="scopeOptions"
                appearance="field"
                size="sm"
                :disabled="scopeLocked"
              />
            </div>
            <AppMultiSelect
              v-if="draft.modes.groups !== 'all'"
              v-model="selectedGroups"
              :label="t('accessKeys.groups')"
              label-hidden
              :placeholder="t('accessKeys.selectGroups')"
              :options="groupOptions"
              :disabled="scopeLocked"
              :loading="groups.isFetching.value"
              :error="fieldError('groups')"
              ><template #option="{ option }"
                ><AppChannelIcon
                  v-if="groupMap.get(option.value)"
                  :icon="groupMap.get(option.value)!.channelIcon"
                  :name="groupMap.get(option.value)!.channelName"
                  :mark="groupMap.get(option.value)!.channelMark"
                  size="sm"
                /><span>{{ option.label }}</span></template
              ></AppMultiSelect
            >
            <div class="modern-access-setting">
              <span>{{ t('accessKeys.protocols') }}</span
              ><AppSegmentedControl
                v-model="draft.modes.protocols"
                :label="t('accessKeys.protocols')"
                :options="scopeOptions"
                appearance="field"
                size="sm"
                :disabled="scopeLocked"
              />
            </div>
            <AppMultiSelect
              v-if="draft.modes.protocols !== 'all'"
              v-model="draft.scope.protocols"
              :label="t('accessKeys.protocols')"
              label-hidden
              :placeholder="t('accessKeys.selectProtocols')"
              :options="protocolOptions"
              :disabled="scopeLocked"
              :error="fieldError('protocols')"
            >
              <template #option="{ option }"><AppProtocolTag :protocol="option.value" /></template>
              <template #tag="{ value, remove: removeTag }"
                ><AppProtocolTag
                  :protocol="value"
                  size="sm"
                  removable
                  :disabled="scopeLocked"
                  @remove="removeTag"
              /></template>
            </AppMultiSelect>
            <div class="modern-access-setting">
              <span>{{ t('accessKeys.models') }}</span
              ><AppSegmentedControl
                v-model="draft.modes.models"
                :label="t('accessKeys.models')"
                :options="scopeOptions"
                appearance="field"
                size="sm"
                :disabled="scopeLocked"
              />
            </div>
            <AppMultiSelect
              v-if="draft.modes.models !== 'all'"
              v-model="draft.scope.models"
              :label="t('accessKeys.models')"
              label-hidden
              :placeholder="t('accessKeys.selectModels')"
              :options="modelOptions"
              allow-custom
              :disabled="scopeLocked"
              :error="fieldError('models')"
            />
            <p v-if="modelMismatch" class="modern-access-note">{{ t('accessKeys.mismatch') }}</p>
          </AppFormSection>
          <AccessKeyQuotaEditor
            v-model="draft.rules"
            :runtime="base?.cost_limit_status"
            :disabled="pending"
            :reset-disabled="dirty"
            :error="fieldError('rules')"
            @reset="openReset"
          />
          <AppFormSection :title="t('accessKeys.policy')" compact>
            <div class="modern-access-field-pair">
              <AppTextField
                v-model="draft.rpm"
                :label="t('accessKeys.rpm')"
                :description="t('accessKeys.rpmHelp')"
                inputmode="numeric"
                size="sm"
                :disabled="pending"
                :error="fieldError('rpm')"
              /><AppTextField
                v-model="draft.price"
                :label="t('accessKeys.multiplier')"
                inputmode="decimal"
                size="sm"
                :disabled="pending"
                :error="fieldError('price')"
              />
            </div>
            <div class="modern-access-setting">
              <span>{{ t('accessKeys.source') }}</span
              ><AppSegmentedControl
                v-model="draft.source"
                :label="t('accessKeys.source')"
                :options="[
                  { value: 'all', label: t('accessKeys.sourceAll') },
                  { value: 'specified', label: t('accessKeys.sourceSpecified') },
                ]"
                appearance="field"
                size="sm"
                :disabled="pending"
              />
            </div>
            <AppTextArea
              v-if="draft.source !== 'all'"
              v-model="draft.cidrs"
              :label="t('accessKeys.cidrs')"
              :description="t('accessKeys.cidrHelp')"
              :rows="3"
              :disabled="pending"
              :error="fieldError('cidrs')"
              placeholder="192.0.2.0/24"
              spellcheck="false"
            />
          </AppFormSection>
          <dl v-if="base" class="modern-access-timestamps">
            <div>
              <dt>{{ t('accessKeys.createdAt') }}</dt>
              <dd>{{ accessTime(base.created_at_ms, locale) }}</dd>
            </div>
            <div>
              <dt>{{ t('accessKeys.updatedAt') }}</dt>
              <dd>{{ accessTime(base.updated_at_ms, locale) }}</dd>
            </div>
          </dl>
        </div>
        <footer class="modern-access-editor-footer">
          <AppIconButton
            v-if="base"
            :icon="Trash2"
            :label="t('accessKeys.delete')"
            variant="danger"
            size="sm"
            :disabled="pending"
            @click="openDelete"
          />
          <div class="modern-access-editor-actions">
            <AppButton v-if="dirty" size="sm" variant="ghost" :disabled="pending" @click="revert">{{
              t('groupDetail.revert')
            }}</AppButton
            ><AppButton
              type="submit"
              variant="primary"
              size="sm"
              :loading="pending"
              :disabled="revealing || !dirty"
              >{{ t(base ? 'groups.edit.save' : 'accessKeys.create') }}</AppButton
            >
          </div>
        </footer>
      </form>
    </AppDialogContent>
  </DialogRoot>
  <AppDraftGuard
    ref="guard"
    :dirty="dirty"
    :pending="pending"
    :query-scope="['panel', 'access_key', 'copy_from']"
  />
  <AppConfirmDialog
    :open="deleteOpen"
    :title="t('accessKeys.delete')"
    :subject="base?.name"
    :description="t('accessKeys.deleteHelp')"
    :confirm-label="t('accessKeys.delete')"
    tone="danger"
    :pending="pending"
    :error="error"
    @cancel="deleteOpen = false"
    @confirm="remove"
  />
  <AccessKeyQuotaResetDialog
    v-if="resetOpen && base"
    :row="base"
    @pending="pending = $event"
    @close="resetOpen = false"
    @reset="quotaReset"
  />
</template>
<style scoped>
.modern-access-editor {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.modern-access-editor-body {
  container: modern-access-editor / inline-size;
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-4);
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
  padding: var(--modern-space-5);
}
.modern-access-usage {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--modern-space-3);
  margin: 0;
}
.modern-access-usage dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-access-usage dd {
  margin: var(--modern-space-1) 0 0;
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-access-note {
  margin: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-access-current-key {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  min-width: 0;
  overflow-wrap: anywhere;
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-secondary);
}
.modern-access-current-key > :first-child {
  min-width: 0;
}
.modern-access-setting,
.modern-access-secret-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-access-secret-heading {
  margin-bottom: calc(-1 * var(--modern-space-2));
}
.modern-access-field-pair {
  display: grid;
  align-items: start;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-3);
}
.modern-access-timestamps {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
  margin: 0;
  padding-top: var(--modern-space-4);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-access-timestamps > div {
  display: flex;
  gap: var(--modern-space-2);
}
.modern-access-timestamps dd {
  margin: 0;
}
.modern-access-editor-footer {
  display: flex;
  align-items: center;
  flex: none;
  gap: var(--modern-space-2);
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-3) var(--modern-space-5);
}
.modern-access-editor-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  margin-left: auto;
  white-space: nowrap;
}
@container modern-access-editor (max-width: 380px) {
  .modern-access-field-pair {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
