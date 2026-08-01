<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupSettingsDto } from '@/api/control/types'

import { ApiError, RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import {
  cacheGroupSettings,
  groupSettingsQueryOptions,
  invalidateGroupSummary,
  updateGroupSettings,
} from '@/app/resources/groups'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SectionNav from '@/components/ui/SectionNav.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'

import GroupDeleteDialog from './GroupDeleteDialog.vue'
import GroupSettingsBaseForm from './GroupSettingsBaseForm.vue'
import {
  buildGroupSettingsPatch,
  createGroupSettingsDraft,
  setGroupConfigOverride,
  type GroupSettingsDraft,
  type GroupTimeoutKey,
} from './group-settings-patch'

const props = defineProps<{ groupId: number }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const query = useQuery(groupSettingsQueryOptions(client, () => props.groupId))
const saved = ref<GroupSettingsDto>()
const draft = ref<GroupSettingsDraft>()
const pending = ref(false)
const deletePending = ref(false)
const deleted = ref(false)
const error = ref('')
const confirmURL = ref(false)
const headerRulesValid = ref(true)
const headerRulesEditorRevision = ref(0)
const section = ref('settings-general')
let controller: AbortController | undefined
const timeoutKeys: GroupTimeoutKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
const navItems = computed(() => [
  { id: 'settings-general', label: t('group.settings.sections.general') },
  { id: 'settings-routing', label: t('group.settings.sections.routing') },
  { id: 'settings-runtime', label: t('group.settings.sections.runtime') },
  { id: 'settings-headers', label: t('group.settings.sections.headers') },
  { id: 'settings-danger', label: t('group.settings.sections.danger') },
])
const patch = computed(() =>
  saved.value && draft.value ? buildGroupSettingsPatch(saved.value, draft.value) : {},
)
const dirty = computed(() => !deleted.value && Object.keys(patch.value).length > 0)
const mutationPending = computed(() => pending.value || deletePending.value)
const nameError = computed(() =>
  draft.value?.name.trim() ? '' : t('group.settings.base.nameError'),
)
const urlError = computed(() => {
  const value = draft.value?.upstream_url.trim() ?? ''
  if (!value) return t('group.settings.base.upstreamUrlError')
  try {
    new URL(value)
    return ''
  } catch {
    return t('group.settings.base.upstreamUrlError')
  }
})
const protocolsError = computed(() =>
  draft.value?.protocols.length ? '' : t('group.settings.base.protocolsError'),
)
const timeoutValid = computed(() =>
  timeoutKeys.every((key) => {
    const value = draft.value?.overrides[key]
    return value === undefined || (Number.isSafeInteger(value) && value > 0)
  }),
)
const valid = computed(
  () =>
    !nameError.value &&
    !urlError.value &&
    !protocolsError.value &&
    timeoutValid.value &&
    headerRulesValid.value,
)
const showInjectUsage = computed(
  () => draft.value?.protocols.includes('openai-completions') ?? false,
)
useUnsavedChanges(dirty, { blocked: mutationPending })

function resetSavedDraft(settings: GroupSettingsDto): void {
  saved.value = settings
  draft.value = createGroupSettingsDraft(settings)
  headerRulesValid.value = true
  headerRulesEditorRevision.value += 1
}

watch(
  () => query.data.value,
  (settings) => {
    if (!settings || dirty.value || mutationPending.value || deleted.value) return
    resetSavedDraft(settings)
  },
  { immediate: true },
)

function setSection(id: string): void {
  section.value = id
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function toggleProtocol(protocol: GroupSettingsDraft['protocols'][number], checked: boolean): void {
  if (!draft.value) return
  const protocols = checked
    ? [...new Set([...draft.value.protocols, protocol])]
    : draft.value.protocols.filter((value) => value !== protocol)
  const overrides = { ...draft.value.overrides }
  if (!protocols.includes('openai-completions')) delete overrides.inject_usage_options
  draft.value = { ...draft.value, protocols, overrides }
}

function setTimeoutOverride(key: GroupTimeoutKey, enabled: boolean): void {
  if (!draft.value || !saved.value) return
  draft.value = setGroupConfigOverride(draft.value, key, enabled, saved.value.effective[key])
}

function setTimeoutValue(key: GroupTimeoutKey, event: Event): void {
  if (!draft.value) return
  const overrides = {
    ...draft.value.overrides,
    [key]: Number((event.target as HTMLInputElement).value),
  }
  draft.value = { ...draft.value, overrides }
}

function setHeaderRules(enabled: boolean): void {
  if (!draft.value || !saved.value) return
  const overrides = { ...draft.value.overrides }
  if (enabled)
    overrides.header_rules = {
      set: { ...saved.value.effective.header_rules.set },
      remove: [...saved.value.effective.header_rules.remove],
    }
  else {
    delete overrides.header_rules
    headerRulesValid.value = true
  }
  draft.value = { ...draft.value, overrides }
}

function setInjectUsageOverride(enabled: boolean): void {
  if (!draft.value || !saved.value) return
  const overrides = { ...draft.value.overrides }
  if (enabled) overrides.inject_usage_options = saved.value.effective.inject_usage_options
  else delete overrides.inject_usage_options
  draft.value = { ...draft.value, overrides }
}

function requestSave(): void {
  if (!dirty.value || !valid.value || mutationPending.value) return
  void save(false)
}

async function save(confirmUpstreamChange: boolean): Promise<void> {
  if (!saved.value || !draft.value || mutationPending.value || !valid.value) return
  const active = new AbortController()
  controller = active
  pending.value = true
  error.value = ''
  try {
    const body = {
      ...patch.value,
      ...(confirmUpstreamChange ? { confirm_upstream_change: true as const } : {}),
    }
    const result = await updateGroupSettings(client, props.groupId, body, active.signal)
    if (controller !== active) return
    resetSavedDraft(result)
    confirmURL.value = false
    cacheGroupSettings(queryClient, props.groupId, result)
    await invalidateGroupSummary(queryClient, props.groupId)
  } catch (cause: unknown) {
    if (cause instanceof RequestCancelledError || controller !== active) return
    if (
      cause instanceof ApiError &&
      cause.code === 'UPSTREAM_CHANGE_CONFIRMATION_REQUIRED' &&
      !confirmUpstreamChange
    )
      confirmURL.value = true
    else error.value = t('group.settings.saveFailed')
  } finally {
    if (controller === active) {
      controller = undefined
      pending.value = false
    }
  }
}

function discard(): void {
  if (!saved.value || mutationPending.value) return
  error.value = ''
  resetSavedDraft(saved.value)
}

function onDeleted(): void {
  deleted.value = true
  confirmURL.value = false
  error.value = ''
}

function headerSummary(): string {
  const rules = draft.value?.overrides.header_rules ?? saved.value?.effective.header_rules
  return rules
    ? t('group.settings.runtime.headerSummary', {
        set: Object.keys(rules.set).length,
        remove: rules.remove.length,
      })
    : ''
}

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="group-settings" aria-labelledby="group-settings-heading">
    <QueryFeedback
      v-if="query.isPending.value && !query.data.value"
      state="loading"
      :message="t('group.settings.loading')"
    />
    <QueryFeedback
      v-else-if="query.isError.value && !query.data.value"
      state="error"
      :message="t('group.settings.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="query.refetch()"
    />
    <template v-else-if="saved && draft">
      <header class="group-settings__header">
        <div>
          <h2 id="group-settings-heading">{{ t('group.settings.title') }}</h2>
          <p>{{ t('group.settings.description') }}</p>
        </div>
      </header>
      <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback>
      <div class="group-settings__layout">
        <SectionNav
          v-model="section"
          :items="navItems"
          :label="t('group.settings.sectionNav')"
          @update:model-value="setSection"
        />
        <div class="group-settings__content">
          <GroupSettingsBaseForm
            section="general"
            :name="draft.name"
            :upstream-url="draft.upstream_url"
            :validation-model="draft.validation_model"
            :weight-manual="draft.weight_manual"
            :protocols="draft.protocols"
            :enabled="draft.enabled"
            :pending="mutationPending"
            :name-error="nameError"
            :upstream-url-error="urlError"
            :protocols-error="protocolsError"
            @update:name="draft.name = $event"
            @update:upstream-url="draft.upstream_url = $event"
            @update:validation-model="draft.validation_model = $event"
            @update:weight-manual="draft.weight_manual = $event"
            @toggle-protocol="toggleProtocol"
            @update:enabled="draft.enabled = $event"
          />
          <GroupSettingsBaseForm
            section="routing"
            :name="draft.name"
            :upstream-url="draft.upstream_url"
            :validation-model="draft.validation_model"
            :weight-manual="draft.weight_manual"
            :protocols="draft.protocols"
            :enabled="draft.enabled"
            :pending="mutationPending"
            :name-error="nameError"
            :upstream-url-error="urlError"
            :protocols-error="protocolsError"
            @update:name="draft.name = $event"
            @update:upstream-url="draft.upstream_url = $event"
            @update:validation-model="draft.validation_model = $event"
            @update:weight-manual="draft.weight_manual = $event"
            @toggle-protocol="toggleProtocol"
            @update:enabled="draft.enabled = $event"
          />
          <section id="settings-runtime" class="group-settings__section">
            <header>
              <h3>{{ t('group.settings.sections.runtime') }}</h3>
              <p>{{ t('group.settings.runtime.description') }}</p>
            </header>
            <div class="group-settings__runtime">
              <div v-for="key in timeoutKeys" :key="key" class="group-settings__runtime-row">
                <div>
                  <strong>{{ t(`group.settings.runtime.${key}`) }}</strong
                  ><small>{{
                    t('group.settings.runtime.effective', { value: saved.effective[key] })
                  }}</small>
                </div>
                <label
                  ><input
                    type="checkbox"
                    :checked="draft.overrides[key] !== undefined"
                    :disabled="mutationPending"
                    @change="setTimeoutOverride(key, ($event.target as HTMLInputElement).checked)"
                  />{{
                    draft.overrides[key] === undefined
                      ? t('group.settings.runtime.inherited')
                      : t('group.settings.runtime.override')
                  }}</label
                ><input
                  v-if="draft.overrides[key] !== undefined"
                  type="number"
                  min="1"
                  :aria-label="
                    t('group.settings.runtime.valueFor', {
                      field: t(`group.settings.runtime.${key}`),
                    })
                  "
                  :value="draft.overrides[key]"
                  :disabled="mutationPending"
                  @input="setTimeoutValue(key, $event)"
                />
              </div>
              <div v-if="showInjectUsage" class="group-settings__runtime-row">
                <div>
                  <strong>{{ t('group.settings.runtime.inject_usage_options') }}</strong
                  ><small>{{ t('group.settings.runtime.injectUsageHelp') }}</small>
                </div>
                <label
                  ><input
                    type="checkbox"
                    :checked="draft.overrides.inject_usage_options !== undefined"
                    :disabled="mutationPending"
                    @change="setInjectUsageOverride(($event.target as HTMLInputElement).checked)"
                  />{{
                    draft.overrides.inject_usage_options === undefined
                      ? t('group.settings.runtime.inherited')
                      : t('group.settings.runtime.override')
                  }}</label
                ><input
                  v-if="draft.overrides.inject_usage_options !== undefined"
                  type="checkbox"
                  :aria-label="
                    t('group.settings.runtime.valueFor', {
                      field: t('group.settings.runtime.inject_usage_options'),
                    })
                  "
                  :checked="draft.overrides.inject_usage_options"
                  :disabled="mutationPending"
                  @change="
                    draft.overrides.inject_usage_options = (
                      $event.target as HTMLInputElement
                    ).checked
                  "
                />
              </div>
            </div>
          </section>
          <section id="settings-headers" class="group-settings__section">
            <header>
              <h3>{{ t('group.settings.sections.headers') }}</h3>
              <p>{{ t('group.settings.headers.description') }}</p>
            </header>
            <details>
              <summary>{{ headerSummary() }}</summary>
              <div class="group-settings__header-controls">
                <label
                  ><input
                    type="checkbox"
                    :checked="draft.overrides.header_rules !== undefined"
                    :disabled="mutationPending"
                    @change="setHeaderRules(($event.target as HTMLInputElement).checked)"
                  />{{
                    draft.overrides.header_rules === undefined
                      ? t('group.settings.runtime.inherited')
                      : t('group.settings.runtime.override')
                  }}</label
                ><InlineFeedback tone="warning">{{
                  t('group.settings.runtime.headerReplacementWarning')
                }}</InlineFeedback
                ><HeaderRulesEditor
                  v-if="draft.overrides.header_rules"
                  :key="headerRulesEditorRevision"
                  :model-value="draft.overrides.header_rules"
                  :disabled="mutationPending"
                  @update:valid="headerRulesValid = $event"
                  @update:model-value="draft.overrides.header_rules = $event"
                />
              </div>
            </details>
          </section>
          <section id="settings-danger" class="group-settings__section group-settings__danger">
            <header>
              <h3>{{ t('group.settings.sections.danger') }}</h3>
              <p>{{ t('group.settings.delete.sectionDescription') }}</p>
            </header>
            <GroupDeleteDialog
              :group-id="groupId"
              :group-name="saved.name"
              :disabled="mutationPending || deleted"
              @deleted="onDeleted"
              @update:pending="deletePending = $event"
            />
          </section>
        </div>
      </div>
      <AppConfirmDialog
        :open="confirmURL"
        :title="t('group.settings.urlConfirm.title')"
        :description="t('group.settings.urlConfirm.description')"
        :close-label="t('group.settings.urlConfirm.close')"
        :cancel-label="t('group.settings.urlConfirm.cancel')"
        :confirm-label="t('group.settings.urlConfirm.confirm')"
        :pending="mutationPending"
        :confirm-disabled="deletePending"
        @update:open="confirmURL = $event"
        @confirm="save(true)"
      />
      <StickySaveBar
        :dirty="dirty"
        :pending="mutationPending"
        :status="error ? 'error' : 'idle'"
        :error="error"
        ><template #status
          ><span>{{
            dirty ? t('group.settings.unsaved') : t('group.settings.saved')
          }}</span></template
        ><template #discard="{ disabled }"
          ><AppButton
            variant="secondary"
            :disabled="disabled || !dirty || deletePending"
            @click="discard"
            >{{ t('common.discard') }}</AppButton
          ></template
        ><template #save="{ disabled }"
          ><AppButton
            :disabled="disabled || !dirty || !valid || deletePending"
            @click="requestSave"
            >{{ t('group.settings.save') }}</AppButton
          ></template
        ></StickySaveBar
      >
    </template>
  </section>
</template>

<style scoped>
.group-settings {
  display: grid;
  gap: var(--space-4);
}
.group-settings__header h2,
.group-settings__header p {
  margin: 0;
}
.group-settings__header p,
.group-settings__section header p,
small {
  color: var(--color-text-muted);
}
.group-settings__layout {
  display: grid;
  grid-template-columns: 180px minmax(0, 1fr);
  gap: var(--space-6);
}
.group-settings__content {
  display: grid;
  gap: var(--space-6);
}
.group-settings__section {
  display: grid;
  gap: var(--space-4);
  border-bottom: 1px solid var(--color-border-subtle);
  padding-bottom: var(--space-6);
}
.group-settings__section header h3,
.group-settings__section header p {
  margin: 0;
}
.group-settings__runtime {
  display: grid;
  gap: var(--space-2);
}
.group-settings__runtime-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 120px;
  align-items: center;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.group-settings__runtime-row div {
  display: grid;
  gap: var(--space-1);
}
.group-settings__runtime-row input[type='number'] {
  min-height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: 0 var(--space-2);
  font: var(--text-sm) var(--font-mono);
}
.group-settings__header-controls {
  display: grid;
  gap: var(--space-3);
  margin-top: var(--space-3);
}
.group-settings__danger {
  border-color: var(--color-danger);
}
@media (max-width: 759px) {
  .group-settings__layout {
    grid-template-columns: 1fr;
  }
  .group-settings__runtime-row {
    grid-template-columns: 1fr;
  }
}
</style>
