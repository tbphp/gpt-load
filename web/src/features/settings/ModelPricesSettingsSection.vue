<script setup lang="ts">
import { ArrowRight, RefreshCw } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { modelPricesLocation, modelsLocation } from '@/app/route-locations'
import { useModelPriceSync } from '@/app/use-model-price-sync'
import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import RuntimeOverrideRow from '@/components/config/RuntimeOverrideRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import { createSettingsDraft, setSettingsOverride, type SettingsDraft } from './settings-patch'
import type { SettingsMergeConflict } from './settings-response'
import type { SettingsDraftChange } from './use-settings-controller'

const settingKey = 'models_dev_auto_sync_enabled' as const
const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  conflicts: SettingsMergeConflict[]
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
  chooseMine: [key: RuntimeSettingKey]
  chooseLatest: [key: RuntimeSettingKey]
}>()
const { t } = useI18n()
const {
  pending: syncPending,
  failed: syncFailure,
  succeeded: syncSucceeded,
  run: runSync,
} = useModelPriceSync()

const readOnly = computed(() => props.draft.readOnly.has(settingKey))
const owned = computed(() => props.draft.overrides.has(settingKey))
const pendingRestore = computed(
  () => !owned.value && props.base.settings.overrides.includes(settingKey),
)
const conflict = computed(() => props.conflicts.find((candidate) => candidate.key === settingKey))

function cloneDraft(): SettingsDraft {
  return createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
}

function setOwned(enabled: boolean): void {
  if (readOnly.value) return
  emit('change', {
    key: settingKey,
    draft: setSettingsOverride(props.base.settings, props.draft, settingKey, enabled),
  })
}

function setValue(value: boolean): void {
  if (readOnly.value) return
  const draft = cloneDraft()
  draft.values.models_dev_auto_sync_enabled = value
  emit('change', { key: settingKey, draft })
}

function conflictValue(side: 'mine' | 'latest'): string {
  const value = conflict.value?.[side]
  if (!value) return ''
  if (value.is_read_only) return t('settings.modelPrices.environmentSource')
  if (!value.is_override) return t('settings.runtime.defaultSource')
  return value.normalized_value ? t('settings.runtime.enabled') : t('settings.runtime.disabled')
}
</script>

<template>
  <section id="settings-model-prices" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.modelPrices.title') }}</h2>
      <p>{{ t('settings.modelPrices.description') }}</p>
    </header>

    <RuntimeOverrideRow
      appearance="ledger"
      :label="t('settings.modelPrices.autoSync')"
      :detail="
        readOnly
          ? t('settings.modelPrices.environmentDetail')
          : owned
            ? t('settings.runtime.overrideValue')
            : pendingRestore
              ? t('settings.runtime.resetPending')
              : t('settings.modelPrices.effectiveDetail')
      "
      :value-label="
        readOnly
          ? t('settings.modelPrices.readOnly')
          : owned || pendingRestore
            ? undefined
            : t('settings.runtime.currentEffective')
      "
      :source-label="
        readOnly
          ? t('settings.modelPrices.environmentSource')
          : owned
            ? t('settings.runtime.overrideSource')
            : t('settings.runtime.defaultSource')
      "
      :action-label="
        readOnly
          ? t('settings.modelPrices.environmentControlled')
          : owned
            ? t('settings.runtime.restoreDefault')
            : t('settings.runtime.override')
      "
      :overridden="owned"
      :divided="false"
      :disabled="disabled || readOnly"
      @toggle="setOwned(!owned)"
    >
      <template #value>
        <div class="model-prices-setting__value">
          <AppSwitch
            v-if="owned && !readOnly"
            :model-value="draft.values.models_dev_auto_sync_enabled"
            :disabled="disabled"
            :label="t('settings.modelPrices.autoSync')"
            @update:model-value="setValue"
          />
          <strong v-else-if="pendingRestore && !readOnly">
            {{ t('settings.runtime.resetPending') }}
          </strong>
          <strong v-else>
            {{
              base.settings.values.models_dev_auto_sync_enabled
                ? t('settings.runtime.enabled')
                : t('settings.runtime.disabled')
            }}
          </strong>
          <small v-if="readOnly">{{ t('settings.modelPrices.readOnlyReason') }}</small>
          <small v-else>{{ t('settings.modelPrices.autoSyncHelp') }}</small>
        </div>
      </template>
    </RuntimeOverrideRow>

    <article v-if="conflict" class="model-prices-setting__conflict" role="alert">
      <strong>{{ t('settings.modelPrices.autoSync') }}</strong>
      <span>{{ t('settings.conflict.mine') }}: {{ conflictValue('mine') }}</span>
      <span>{{ t('settings.conflict.latest') }}: {{ conflictValue('latest') }}</span>
      <div>
        <AppButton variant="secondary" size="compact" @click="emit('chooseMine', settingKey)">
          {{ t('settings.conflict.useMine') }}
        </AppButton>
        <AppButton variant="ghost" size="compact" @click="emit('chooseLatest', settingKey)">
          {{ t('settings.conflict.useLatest') }}
        </AppButton>
      </div>
    </article>

    <div class="model-prices-setting__actions">
      <div>
        <strong>{{ t('settings.modelPrices.manualSync') }}</strong>
        <small>{{ t('settings.modelPrices.manualSyncHelp') }}</small>
      </div>
      <div>
        <AppButton variant="secondary" size="compact" :busy="syncPending" @click="runSync">
          <RefreshCw :size="15" aria-hidden="true" />{{ t('settings.modelPrices.syncNow') }}
        </AppButton>
        <RouterLink v-slot="{ navigate }" :to="modelPricesLocation()" custom>
          <AppButton role="link" variant="secondary" size="compact" @click="navigate">
            {{ t('settings.modelPrices.manage') }}
          </AppButton>
        </RouterLink>
        <RouterLink v-slot="{ navigate }" :to="modelsLocation()" custom>
          <AppButton role="link" size="compact" @click="navigate">
            {{ t('settings.modelPrices.openModels') }}<ArrowRight :size="15" aria-hidden="true" />
          </AppButton>
        </RouterLink>
      </div>
    </div>

    <InlineFeedback v-if="syncSucceeded" appearance="ledger" tone="success">
      {{ t('settings.modelPrices.syncSucceeded') }}
    </InlineFeedback>
    <InlineFeedback v-if="syncFailure" appearance="ledger" tone="danger">
      {{ t('settings.modelPrices.syncFailed') }}
      <template #action>
        <AppButton variant="link" size="inline" @click="runSync">
          {{ t('common.retry') }}
        </AppButton>
      </template>
    </InlineFeedback>
    <InlineFeedback appearance="ledger" tone="neutral">
      {{ t('settings.modelPrices.proxyNote') }}
    </InlineFeedback>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.model-prices-setting__value,
.model-prices-setting__conflict {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading h2,
.settings-section__heading p {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--text-sm);
  font-weight: 650;
}

.settings-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.model-prices-setting__value {
  gap: var(--space-1);
}

.model-prices-setting__value strong {
  font-size: var(--text-sm);
}

.model-prices-setting__value small,
.model-prices-setting__actions small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-prices-setting__conflict {
  gap: var(--space-1);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  font-size: var(--text-label-xs);
}

.model-prices-setting__conflict > div,
.model-prices-setting__actions,
.model-prices-setting__actions > div:last-child {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.model-prices-setting__conflict > div {
  margin-top: var(--space-1);
}

.model-prices-setting__actions {
  justify-content: space-between;
  gap: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  padding: var(--space-3-5) var(--space-0-5) 0;
}

.model-prices-setting__actions > div:first-child {
  display: grid;
  gap: var(--space-1);
}

.model-prices-setting__actions strong {
  font-size: var(--text-meta);
}

@media (max-width: 760px) {
  .model-prices-setting__actions {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
