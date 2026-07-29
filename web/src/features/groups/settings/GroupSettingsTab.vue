<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { Save, TriangleAlert } from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  isUpstreamUrlConflictData,
  updateGroup,
  type GroupDetailDto,
  type UpstreamUrlConflictData,
} from '@/app/resources/groups'
import type { GroupProtocol } from '@/api/control/types'
import { ApiError, RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import GroupDeleteDialog from './GroupDeleteDialog.vue'
import GroupSettingsBaseForm from './GroupSettingsBaseForm.vue'
import {
  buildGroupSettingsPatch,
  createGroupSettingsDraft,
  enableHeaderRulesOverride,
  rebaseGroupSettingsDraft,
  setGroupConfigOverride,
  type GroupSettingsDraft,
  type GroupTimeoutKey,
} from './group-settings-patch'

const props = defineProps<{ groupId: number; group: GroupDetailDto }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const savedGroup = ref(props.group)
const draft = ref<GroupSettingsDraft>(createGroupSettingsDraft(props.group))
const pending = ref(false)
const genericError = ref(false)
const urlConfirmOpen = ref(false)
const urlConflict = ref<UpstreamUrlConflictData | null>(null)
const rediscoveryRecommended = ref(false)
const headerRulesValid = ref(true)
let controller: AbortController | undefined
const saveFocusTarget = ref<HTMLElement | null>(null)
const headingFocusTarget = ref<HTMLElement | null>(null)

const timeoutKeys: GroupTimeoutKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
const patch = computed(() => buildGroupSettingsPatch(savedGroup.value, draft.value))
const dirty = computed(() => Object.keys(patch.value).length > 0)
useUnsavedChanges(dirty, { blocked: pending })
const nameError = computed(() =>
  draft.value.name.trim() === '' ? t('group.settings.base.nameError') : '',
)
const upstreamURLError = computed(() =>
  draft.value.upstream_url.trim() === '' ? t('group.settings.base.upstreamUrlError') : '',
)
const protocolsError = computed(() =>
  draft.value.protocols.length === 0 ? t('group.settings.base.protocolsError') : '',
)

function timeoutError(key: GroupTimeoutKey): string {
  const value = draft.value.config[key]
  return value !== undefined && (!Number.isSafeInteger(value) || value <= 0)
    ? t('group.settings.runtime.timeoutError')
    : ''
}

const valid = computed(() => {
  if (nameError.value || upstreamURLError.value || protocolsError.value) return false
  if (
    draft.value.weight_manual !== null &&
    (!Number.isInteger(draft.value.weight_manual) ||
      draft.value.weight_manual < 0 ||
      draft.value.weight_manual > 100)
  ) {
    return false
  }
  if (draft.value.config.header_rules && !headerRulesValid.value) return false
  return timeoutKeys.every((key) => timeoutError(key) === '')
})

watch(
  () => props.group,
  (group) => {
    if (dirty.value) {
      draft.value = rebaseGroupSettingsDraft(savedGroup.value, draft.value, group)
      savedGroup.value = group
      return
    }
    savedGroup.value = group
    draft.value = createGroupSettingsDraft(group)
  },
)

function hasOverride(key: GroupTimeoutKey | 'header_rules'): boolean {
  return draft.value.config[key] !== undefined
}

function setTimeoutOverride(key: GroupTimeoutKey, enabled: boolean): void {
  draft.value = setGroupConfigOverride(draft.value, key, enabled, savedGroup.value.effective_config)
}

function setTimeoutValue(key: GroupTimeoutKey, event: Event): void {
  const value = Number((event.target as HTMLInputElement).value)
  draft.value = { ...draft.value, config: { ...draft.value.config, [key]: value } }
}

function setHeaderRulesOverride(enabled: boolean): void {
  headerRulesValid.value = true
  const config = { ...draft.value.config }
  if (enabled) {
    config.header_rules = enableHeaderRulesOverride(savedGroup.value.effective_config.header_rules)
  } else {
    delete config.header_rules
  }
  draft.value = { ...draft.value, config }
}

function setHeaderRules(value: NonNullable<GroupSettingsDraft['config']['header_rules']>): void {
  draft.value = {
    ...draft.value,
    config: { ...draft.value.config, header_rules: value },
  }
}

function toggleProtocol(protocol: GroupProtocol, checked: boolean): void {
  draft.value = {
    ...draft.value,
    protocols: checked
      ? [...new Set([...draft.value.protocols, protocol])]
      : draft.value.protocols.filter((value) => value !== protocol),
  }
}

function healthAffected(body: ReturnType<typeof buildGroupSettingsPatch>): boolean {
  return body.name !== undefined || body.enabled !== undefined || body.weight_manual !== undefined
}

async function restoreFocusAfterURLConfirmation(): Promise<void> {
  await nextTick()
  if (saveFocusTarget.value && !saveFocusTarget.value.matches(':disabled')) {
    saveFocusTarget.value.focus()
  } else {
    headingFocusTarget.value?.focus()
  }
}

function setURLConfirmOpen(open: boolean): void {
  if (!open && pending.value) return
  urlConfirmOpen.value = open
}

async function runSave(confirmUpstreamURLChange = false): Promise<void> {
  if (!confirmUpstreamURLChange)
    saveFocusTarget.value = document.activeElement as HTMLElement | null
  if (pending.value || !valid.value) return
  const normalizedPatch = buildGroupSettingsPatch(savedGroup.value, draft.value)
  if (Object.keys(normalizedPatch).length === 0) return

  const body = confirmUpstreamURLChange
    ? { ...normalizedPatch, confirm_upstream_url_change: true as const }
    : normalizedPatch
  pending.value = true
  genericError.value = false
  urlConflict.value = null
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  try {
    const result = await updateGroup(client, props.groupId, body, activeController.signal)
    if (controller !== activeController) return
    await queryClient.cancelQueries({
      queryKey: controlQueryKeys.groups.detail(props.groupId),
      exact: true,
    })
    if (controller !== activeController) return
    savedGroup.value = result.group
    draft.value = createGroupSettingsDraft(result.group)
    urlConfirmOpen.value = false
    rediscoveryRecommended.value =
      rediscoveryRecommended.value || result.model_rediscovery_recommended
    queryClient.setQueryData(controlQueryKeys.groups.detail(props.groupId), result.group)
    await applyInvalidationPlan(
      queryClient,
      mutationInvalidationPlans.group.update(healthAffected(normalizedPatch)),
      () => controller === activeController,
    )
  } catch (error: unknown) {
    if (controller !== activeController || error instanceof RequestCancelledError) return
    if (error instanceof ApiError && error.code === 'UPSTREAM_URL_CHANGE_CONFIRMATION_REQUIRED') {
      urlConfirmOpen.value = true
    } else if (
      error instanceof ApiError &&
      error.code === 'UPSTREAM_URL_CONFLICT' &&
      isUpstreamUrlConflictData(error.data)
    ) {
      urlConflict.value = error.data
    } else {
      genericError.value = true
    }
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
}

function effectiveHeaderSummary(): string {
  const rules = savedGroup.value.effective_config.header_rules
  return t('group.settings.runtime.headerSummary', {
    set: Object.keys(rules.set).length,
    remove: rules.remove.length,
  })
}

watch(urlConfirmOpen, async (open, wasOpen) => {
  if (!open && wasOpen) await restoreFocusAfterURLConfirmation()
})

onBeforeUnmount(() => {
  controller?.abort()
  controller = undefined
})
</script>

<template>
  <section class="group-settings" aria-labelledby="group-settings-heading">
    <header class="group-settings__header">
      <div>
        <h2 id="group-settings-heading" ref="headingFocusTarget" tabindex="-1">
          {{ t('group.settings.title') }}
        </h2>
        <p>{{ t('group.settings.description') }}</p>
      </div>
      <AppButton
        data-test="group-settings-save"
        :busy="pending"
        :disabled="!dirty || !valid"
        @click="runSave(false)"
      >
        <Save :size="16" aria-hidden="true" />{{ t('group.settings.save') }}
      </AppButton>
    </header>

    <InlineFeedback v-if="genericError" tone="danger">
      {{ t('group.settings.saveFailed') }}
    </InlineFeedback>
    <InlineFeedback
      v-if="rediscoveryRecommended"
      data-test="group-rediscovery-recommended"
      tone="warning"
    >
      <span>{{ t('group.settings.rediscoveryRecommended') }}</span>
      <RouterLink
        data-test="group-rediscovery-action"
        class="group-settings__inline-action"
        :to="{ name: 'group-detail', params: { id: groupId }, query: { tab: 'models' } }"
      >
        {{ t('group.settings.rediscoverAction') }}
      </RouterLink>
    </InlineFeedback>
    <div
      v-if="urlConflict"
      data-test="group-url-conflict"
      class="group-settings__conflict"
      role="alert"
    >
      <TriangleAlert :size="18" aria-hidden="true" />
      <div>
        <strong>{{ t('group.settings.urlConflict.title') }}</strong>
        <p>{{ t('group.settings.urlConflict.description') }}</p>
        <ul>
          <li v-for="conflictGroup in urlConflict.groups" :key="conflictGroup.id">
            {{ conflictGroup.name }} (#{{ conflictGroup.id }})
          </li>
        </ul>
      </div>
    </div>

    <GroupSettingsBaseForm
      :name="draft.name"
      :upstream-url="draft.upstream_url"
      :validation-model="draft.validation_model"
      :weight-manual="draft.weight_manual"
      :protocols="draft.protocols"
      :enabled="draft.enabled"
      :pending="pending"
      :name-error="nameError"
      :upstream-url-error="upstreamURLError"
      :protocols-error="protocolsError"
      @update:name="draft.name = $event"
      @update:upstream-url="draft.upstream_url = $event"
      @update:validation-model="draft.validation_model = $event"
      @update:weight-manual="draft.weight_manual = $event"
      @toggle-protocol="toggleProtocol"
      @update:enabled="draft.enabled = $event"
    />

    <SurfaceCard class="group-settings__card">
      <div class="group-settings__section-heading">
        <h3>{{ t('group.settings.runtime.title') }}</h3>
        <p>{{ t('group.settings.runtime.description') }}</p>
      </div>
      <div class="group-settings__runtime-list">
        <div
          v-for="key in timeoutKeys"
          :key="key"
          :data-test="`runtime-${key}`"
          class="group-settings__runtime-row"
        >
          <div>
            <strong>{{ t(`group.settings.runtime.${key}`) }}</strong>
            <p>
              {{
                t('group.settings.runtime.effective', { value: savedGroup.effective_config[key] })
              }}
            </p>
          </div>
          <StatusBadge :tone="hasOverride(key) ? 'warning' : 'neutral'">
            {{
              hasOverride(key)
                ? t('group.settings.runtime.override')
                : t('group.settings.runtime.inherited')
            }}
          </StatusBadge>
          <label class="group-settings__override-toggle">
            <input
              :data-test="`override-${key}`"
              type="checkbox"
              :checked="hasOverride(key)"
              :disabled="pending"
              @change="setTimeoutOverride(key, ($event.target as HTMLInputElement).checked)"
            />
            {{ t('group.settings.runtime.useOverride') }}
          </label>
          <input
            v-if="hasOverride(key)"
            class="group-settings__runtime-input"
            type="number"
            min="1"
            step="1"
            :aria-label="
              t('group.settings.runtime.valueFor', { field: t(`group.settings.runtime.${key}`) })
            "
            :aria-invalid="timeoutError(key) ? 'true' : undefined"
            :aria-describedby="timeoutError(key) ? `runtime-${key}-error` : undefined"
            :value="draft.config[key]"
            :disabled="pending"
            @input="setTimeoutValue(key, $event)"
          />
          <small
            v-if="timeoutError(key)"
            :id="`runtime-${key}-error`"
            :data-test="`runtime-${key}-error`"
            class="group-settings__field-error"
            role="alert"
            >{{ timeoutError(key) }}</small
          >
        </div>

        <div
          data-test="runtime-header_rules"
          class="group-settings__runtime-row group-settings__headers"
        >
          <div>
            <strong>{{ t('group.settings.runtime.header_rules') }}</strong>
            <p>{{ effectiveHeaderSummary() }}</p>
          </div>
          <StatusBadge :tone="hasOverride('header_rules') ? 'warning' : 'neutral'">
            {{
              hasOverride('header_rules')
                ? t('group.settings.runtime.override')
                : t('group.settings.runtime.inherited')
            }}
          </StatusBadge>
          <label class="group-settings__override-toggle">
            <input
              data-test="override-header_rules"
              type="checkbox"
              :checked="hasOverride('header_rules')"
              :disabled="pending"
              @change="setHeaderRulesOverride(($event.target as HTMLInputElement).checked)"
            />
            {{ t('group.settings.runtime.useOverride') }}
          </label>
          <div v-if="draft.config.header_rules" class="group-settings__header-editor">
            <InlineFeedback data-test="header-rules-replacement-warning" tone="warning">
              {{ t('group.settings.runtime.headerReplacementWarning') }}
            </InlineFeedback>
            <HeaderRulesEditor
              v-model:valid="headerRulesValid"
              :model-value="draft.config.header_rules"
              :disabled="pending"
              @update:model-value="setHeaderRules"
            />
          </div>
        </div>
      </div>
    </SurfaceCard>

    <SurfaceCard class="group-settings__card group-settings__danger">
      <div class="group-settings__section-heading">
        <h3>{{ t('group.settings.delete.sectionTitle') }}</h3>
        <p>{{ t('group.settings.delete.sectionDescription') }}</p>
      </div>
      <GroupDeleteDialog :group-id="groupId" :group-name="savedGroup.name" />
    </SurfaceCard>

    <AppDialog
      :open="urlConfirmOpen"
      :title="t('group.settings.urlConfirm.title')"
      :description="t('group.settings.urlConfirm.description')"
      :close-label="t('group.settings.urlConfirm.close')"
      :dismissible="!pending"
      prevent-close-auto-focus
      @update:open="setURLConfirmOpen"
    >
      <div class="group-settings__dialog-actions">
        <AppButton variant="secondary" :disabled="pending" @click="setURLConfirmOpen(false)">
          {{ t('group.settings.urlConfirm.cancel') }}
        </AppButton>
        <AppButton
          data-test="group-url-confirm"
          class="group-settings__confirm-url"
          variant="secondary"
          :busy="pending"
          @click="runSave(true)"
        >
          {{ t('group.settings.urlConfirm.confirm') }}
        </AppButton>
      </div>
    </AppDialog>
  </section>
</template>

<style scoped>
.group-settings {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}
.group-settings__header,
.group-settings__section-heading,
.group-settings__runtime-row,
.group-settings__dialog-actions {
  display: flex;
}
.group-settings__header,
.group-settings__runtime-row {
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
.group-settings__header h2,
.group-settings__header p,
.group-settings__section-heading h3,
.group-settings__section-heading p,
.group-settings__runtime-row p,
.group-settings__conflict p {
  margin: 0;
}
.group-settings__header h2 {
  font-size: 1.125rem;
}
.group-settings__header p,
.group-settings__section-heading p,
.group-settings__runtime-row p,
small {
  color: var(--color-text-muted);
}
.group-settings__field-error {
  color: var(--color-danger);
  font-size: 0.8125rem;
}
.group-settings__header p,
.group-settings__section-heading p {
  margin-top: var(--space-1);
}
.group-settings__header :deep(.app-button) {
  gap: var(--space-2);
}
.group-settings__card,
.group-settings__section-heading,
.group-settings__runtime-list,
.group-settings__header-editor {
  display: grid;
  gap: var(--space-3);
}
.group-settings__section-heading {
  gap: 0;
}
.group-settings input[type='number'] {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
.group-settings__runtime-input {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.group-settings__override-toggle {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}
.group-settings input[type='checkbox'] {
  width: 18px;
  height: 18px;
}
.group-settings__runtime-row {
  display: grid;
  grid-template-columns: minmax(190px, 1fr) auto minmax(145px, auto) minmax(96px, 140px);
  align-items: center;
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-3);
}
.group-settings__runtime-row:first-child {
  border-top: 0;
  padding-top: 0;
}
.group-settings__runtime-row > .group-settings__field-error {
  grid-column: 4;
}
.group-settings__headers .group-settings__header-editor {
  grid-column: 1 / -1;
}
.group-settings__conflict {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-3);
  border: 1px solid color-mix(in srgb, var(--color-danger) 38%, var(--color-border));
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  color: var(--color-danger);
  padding: var(--space-3);
}
.group-settings__conflict ul {
  margin: var(--space-2) 0 0;
}
.group-settings__inline-action {
  margin-left: var(--space-2);
  color: currentColor;
  font-weight: 700;
}
.group-settings__danger {
  border-color: color-mix(in srgb, var(--color-danger) 38%, var(--color-border));
}
.group-settings__dialog-actions {
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
}
.group-settings__confirm-url {
  border-color: var(--color-warning);
  color: var(--color-warning);
}
@media (max-width: 760px) {
  .group-settings__header {
    align-items: stretch;
    flex-direction: column;
  }
  .group-settings__runtime-row {
    grid-template-columns: 1fr auto;
  }
  .group-settings__runtime-row > :nth-child(3),
  .group-settings__runtime-row > :nth-child(4) {
    grid-column: 1 / -1;
  }
  .group-settings__header :deep(.app-button) {
    width: 100%;
  }
}
</style>
