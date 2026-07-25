<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { FileClock, Save } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { updateSettings, type SettingsDto } from '@/api/control/settings'
import { RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import SettingOverrideField from './SettingOverrideField.vue'
import {
  buildSettingsPatch,
  createSettingsDraft,
  isValidRetention,
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
let controller: AbortController | undefined

const patch = computed(() => buildSettingsPatch(base.value, draft.value, 'logs-maintenance'))
const dirty = computed(() => Object.keys(patch.value).length > 0)
const valid = computed(() => validateSettingsSection(draft.value, 'logs-maintenance'))
const owned = computed(() => draft.value.overrides.has('request_log_retention_days'))
const error = computed(() =>
  owned.value && !isValidRetention(draft.value.values.request_log_retention_days)
    ? t('settings.logs.retentionError')
    : undefined,
)

function rebase(settings: SettingsDto): void {
  base.value = settings
  draft.value = createSettingsDraft(settings)
  failed.value = false
}

function acceptExternalSettings(settings: SettingsDto): void {
  if (dirty.value) {
    base.value = settings
    return
  }
  rebase(settings)
}
watch(() => props.settings, acceptExternalSettings)

function setOwned(enabled: boolean): void {
  draft.value = setSettingsOverride(base.value, draft.value, 'request_log_retention_days', enabled)
  succeeded.value = false
}

function setValue(value: number): void {
  draft.value = {
    values: { ...draft.value.values, request_log_retention_days: value },
    overrides: new Set(draft.value.overrides),
  }
  succeeded.value = false
}

async function save(): Promise<void> {
  if (pending.value || !valid.value) return
  const normalizedPatch = buildSettingsPatch(base.value, draft.value, 'logs-maintenance')
  if (Object.keys(normalizedPatch).length === 0) return

  pending.value = true
  failed.value = false
  succeeded.value = false
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  try {
    const settings = await updateSettings(client, normalizedPatch, activeController.signal)
    if (controller !== activeController) return
    rebase(settings)
    succeeded.value = true
    queryClient.setQueryData(controlQueryKeys.settings(), settings)
    await queryClient.invalidateQueries({ queryKey: controlQueryKeys.groups.details() })
  } catch (error: unknown) {
    if (controller !== activeController || error instanceof RequestCancelledError) return
    failed.value = true
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
  <SurfaceCard class="settings-card logs-maintenance">
    <header class="settings-card__heading">
      <div class="settings-card__title">
        <span class="settings-card__icon"><FileClock :size="18" aria-hidden="true" /></span>
        <div>
          <h2>{{ t('settings.logs.title') }}</h2>
          <p>{{ t('settings.logs.description') }}</p>
        </div>
      </div>
      <AppButton
        data-test="logs-maintenance-save"
        :busy="pending"
        :disabled="!dirty || !valid"
        @click="save"
      >
        <Save :size="16" aria-hidden="true" />{{ t('settings.save') }}
      </AppButton>
    </header>
    <InlineFeedback v-if="failed" tone="danger">{{ t('settings.logs.saveFailed') }}</InlineFeedback>
    <InlineFeedback v-if="succeeded" tone="info">{{ t('settings.saved') }}</InlineFeedback>
    <SettingOverrideField
      setting-key="request_log_retention_days"
      :label="t('settings.logs.retention')"
      :description="t('settings.logs.retentionDescription')"
      :effective-value="base.values.request_log_retention_days"
      :owned="owned"
      :model-value="draft.values.request_log_retention_days"
      :error="error"
      :min="1"
      :max="365"
      :disabled="pending"
      @update:owned="setOwned"
      @update:model-value="setValue"
    />
  </SurfaceCard>
</template>

<style scoped>
.settings-card,
.settings-card__title,
.settings-card__heading {
  display: grid;
}
.settings-card {
  gap: var(--space-4);
}
.settings-card__heading {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--space-4);
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
@media (max-width: 640px) {
  .settings-card__heading {
    grid-template-columns: 1fr;
  }
  .settings-card__heading :deep(.app-button) {
    width: 100%;
  }
}
</style>
