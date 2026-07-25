<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { ChevronDown, Save, SlidersHorizontal } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { updateSettings, type SettingsDto, type TimeoutSettingKey } from '@/api/control/settings'
import { RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import SettingOverrideField from './SettingOverrideField.vue'
import {
  buildSettingsPatch,
  createSettingsDraft,
  hasDuplicateHeaderNames,
  isValidTimeout,
  rebaseSettingsDraft,
  setSettingsOverride,
  validateSettingsSection,
  type SettingsDraft,
} from './settings-patch'

const props = defineProps<{ settings: SettingsDto }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const base = ref(props.settings)
const draft = ref<SettingsDraft>(createSettingsDraft(props.settings))
const pending = ref(false)
const failed = ref(false)
const succeeded = ref(false)
const headerSaveError = ref(false)
const disclosureRequested = ref(props.settings.overrides.includes('header_rules'))
const headerValid = ref(!hasDuplicateHeaderNames(props.settings.values.header_rules))
let controller: AbortController | undefined

const timeoutKeys: TimeoutSettingKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
const patch = computed(() => buildSettingsPatch(base.value, draft.value, 'request-forwarding'))
const dirty = computed(() => Object.keys(patch.value).length > 0)
const valid = computed(
  () =>
    (!draft.value.overrides.has('header_rules') || headerValid.value) &&
    validateSettingsSection(draft.value, 'request-forwarding'),
)
const headerDirty = computed(() =>
  Object.prototype.hasOwnProperty.call(patch.value, 'header_rules'),
)
const headerOpen = computed(
  () =>
    disclosureRequested.value ||
    draft.value.overrides.has('header_rules') ||
    headerDirty.value ||
    (!headerValid.value && draft.value.overrides.has('header_rules')) ||
    headerSaveError.value,
)

function rebase(settings: SettingsDto): void {
  base.value = settings
  draft.value = createSettingsDraft(settings)
  headerValid.value = !hasDuplicateHeaderNames(settings.values.header_rules)
  disclosureRequested.value = settings.overrides.includes('header_rules')
  failed.value = false
  headerSaveError.value = false
}

function acceptExternalSettings(settings: SettingsDto): void {
  if (dirty.value) {
    draft.value = rebaseSettingsDraft(base.value, draft.value, settings, 'request-forwarding')
    base.value = settings
    headerValid.value = !hasDuplicateHeaderNames(draft.value.values.header_rules)
    return
  }
  rebase(settings)
}

watch(() => props.settings, acceptExternalSettings)

function hasOverride(key: TimeoutSettingKey | 'header_rules'): boolean {
  return draft.value.overrides.has(key)
}

function setOverride(key: TimeoutSettingKey, enabled: boolean): void {
  draft.value = setSettingsOverride(base.value, draft.value, key, enabled)
  succeeded.value = false
}

function setTimeoutValue(key: TimeoutSettingKey, value: number): void {
  draft.value = {
    values: { ...draft.value.values, [key]: value },
    overrides: new Set(draft.value.overrides),
  }
  succeeded.value = false
}

function timeoutError(key: TimeoutSettingKey): string | undefined {
  return hasOverride(key) && !isValidTimeout(draft.value.values[key])
    ? t('settings.request.timeoutError')
    : undefined
}

function setHeaderOverride(enabled: boolean): void {
  draft.value = setSettingsOverride(base.value, draft.value, 'header_rules', enabled)
  if (enabled) disclosureRequested.value = true
  succeeded.value = false
}

function setHeaderRules(value: SettingsDto['values']['header_rules']): void {
  draft.value = {
    values: { ...draft.value.values, header_rules: value },
    overrides: new Set(draft.value.overrides),
  }
  succeeded.value = false
}

function toggleDisclosure(): void {
  disclosureRequested.value = !headerOpen.value
}

async function save(): Promise<void> {
  if (pending.value || !valid.value) return
  const normalizedPatch = buildSettingsPatch(base.value, draft.value, 'request-forwarding')
  if (Object.keys(normalizedPatch).length === 0) return

  pending.value = true
  failed.value = false
  succeeded.value = false
  headerSaveError.value = false
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  try {
    const settings = await updateSettings(client, normalizedPatch, activeController.signal)
    if (controller !== activeController) return
    await queryClient.cancelQueries({ queryKey: controlQueryKeys.settings(), exact: true })
    if (controller !== activeController) return
    rebase(settings)
    succeeded.value = true
    queryClient.setQueryData(controlQueryKeys.settings(), settings)
    await queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.details() })
  } catch (error: unknown) {
    if (controller !== activeController || error instanceof RequestCancelledError) return
    failed.value = true
    headerSaveError.value = Object.prototype.hasOwnProperty.call(normalizedPatch, 'header_rules')
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
}

onBeforeUnmount(() => {
  controller?.abort()
  controller = undefined
})
</script>

<template>
  <SurfaceCard class="settings-card request-forwarding">
    <header class="settings-card__heading settings-card__heading--actions">
      <div class="settings-card__title">
        <span class="settings-card__icon"><SlidersHorizontal :size="18" aria-hidden="true" /></span>
        <div>
          <h2>{{ t('settings.request.title') }}</h2>
          <p>{{ t('settings.request.description') }}</p>
        </div>
      </div>
      <AppButton
        data-test="request-forwarding-save"
        :busy="pending"
        :disabled="!dirty || !valid"
        @click="save"
      >
        <Save :size="16" aria-hidden="true" />{{ t('settings.save') }}
      </AppButton>
    </header>

    <InlineFeedback v-if="failed" tone="danger">{{
      t('settings.request.saveFailed')
    }}</InlineFeedback>
    <InlineFeedback v-if="succeeded" tone="info">{{ t('settings.saved') }}</InlineFeedback>
    <InlineFeedback tone="info">{{ t('settings.lastWriteWins') }}</InlineFeedback>

    <div class="request-forwarding__fields">
      <SettingOverrideField
        v-for="key in timeoutKeys"
        :key="key"
        :setting-key="key"
        :label="t(`settings.request.${key}`)"
        :description="t('settings.request.seconds')"
        :effective-value="base.values[key]"
        :owned="hasOverride(key)"
        :model-value="draft.values[key]"
        :error="timeoutError(key)"
        :min="1"
        :disabled="pending"
        @update:owned="setOverride(key, $event)"
        @update:model-value="setTimeoutValue(key, $event)"
      />
    </div>

    <section class="request-forwarding__advanced">
      <button
        data-test="settings-header-disclosure"
        class="request-forwarding__disclosure"
        type="button"
        :aria-expanded="headerOpen"
        aria-controls="settings-header-rules"
        @click="toggleDisclosure"
      >
        <span>
          <strong>{{ t('settings.request.headerRules') }}</strong>
          <small>{{
            t('settings.request.headerSummary', {
              set: Object.keys(base.values.header_rules.set).length,
              remove: base.values.header_rules.remove.length,
            })
          }}</small>
        </span>
        <StatusBadge :tone="hasOverride('header_rules') ? 'warning' : 'neutral'">
          {{ hasOverride('header_rules') ? t('settings.override') : t('settings.default') }}
        </StatusBadge>
        <ChevronDown :size="18" aria-hidden="true" :class="{ rotated: headerOpen }" />
      </button>

      <div v-if="headerOpen" id="settings-header-rules" class="request-forwarding__advanced-body">
        <label class="request-forwarding__header-toggle">
          <input
            data-test="override-header_rules"
            type="checkbox"
            :checked="hasOverride('header_rules')"
            :disabled="pending"
            @change="setHeaderOverride(($event.target as HTMLInputElement).checked)"
          />
          {{ t('settings.useOverride') }}
        </label>
        <InlineFeedback tone="warning">{{ t('settings.request.headerWarning') }}</InlineFeedback>
        <div v-if="hasOverride('header_rules')" data-test="header-rules-editor">
          <HeaderRulesEditor
            :model-value="draft.values.header_rules"
            :disabled="pending"
            @update:model-value="setHeaderRules"
            @update:valid="headerValid = $event"
          />
        </div>
      </div>
    </section>
  </SurfaceCard>
</template>

<style scoped>
.settings-card,
.settings-card__title,
.settings-card__heading,
.request-forwarding__fields,
.request-forwarding__advanced,
.request-forwarding__advanced-body {
  display: grid;
}
.settings-card {
  gap: var(--space-4);
}
.settings-card__heading {
  gap: var(--space-4);
}
.settings-card__heading--actions {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
}
.settings-card__title {
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-3);
}
.settings-card__heading h2,
.settings-card__heading p {
  margin: 0;
}
.settings-card__heading h2 {
  font-size: 1rem;
}
.settings-card__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.settings-card__icon {
  display: inline-flex;
  width: 36px;
  height: 36px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-control);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.settings-card__heading :deep(.app-button) {
  gap: var(--space-2);
}
.request-forwarding__fields,
.request-forwarding__advanced-body {
  gap: var(--space-4);
}
.request-forwarding__advanced {
  gap: var(--space-3);
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-4);
}
.request-forwarding__disclosure {
  display: grid;
  min-height: 44px;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
  border: 0;
  background: transparent;
  color: var(--color-text);
  padding: 0;
  text-align: left;
  font: inherit;
  cursor: pointer;
}
.request-forwarding__disclosure span:first-child {
  display: grid;
  gap: var(--space-1);
}
.request-forwarding__disclosure small {
  color: var(--color-text-muted);
}
.request-forwarding__disclosure svg {
  transition: transform var(--duration-fast) ease;
}
.request-forwarding__disclosure svg.rotated {
  transform: rotate(180deg);
}
.request-forwarding__header-toggle {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}
.request-forwarding__header-toggle input {
  width: 18px;
  height: 18px;
}
@media (max-width: 640px) {
  .settings-card__heading--actions {
    grid-template-columns: 1fr;
  }
  .settings-card__heading :deep(.app-button) {
    width: 100%;
  }
  .request-forwarding__disclosure {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .request-forwarding__disclosure :deep(.status-badge) {
    grid-column: 1;
    width: fit-content;
  }
  .request-forwarding__disclosure > svg {
    grid-column: 2;
    grid-row: 1;
  }
}
@media (prefers-reduced-motion: reduce) {
  .request-forwarding__disclosure svg {
    transition: none;
  }
}
</style>
