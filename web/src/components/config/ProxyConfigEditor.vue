<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProxyConfiguredMode, ProxyMutation, ProxyViewDto } from '@/api/control/types'
import { proxyMutation } from '@/app/resources/proxy'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = withDefaults(
  defineProps<{
    scope: 'global' | 'group' | 'credential'
    view: ProxyViewDto
    saveProxy: (value: ProxyMutation) => Promise<void>
    supported?: boolean
    disabled?: boolean
    divided?: boolean
  }>(),
  { supported: true, disabled: false, divided: true },
)

const { t } = useI18n()
const generatedId = useId()
const inputId = `${generatedId}-proxy-url`
const editing = ref(false)
const pending = ref(false)
const mode = ref<ProxyConfiguredMode>('inherit')
const endpoint = ref('')
const touched = ref(false)
const saveFailed = ref(false)

const modeOptions = computed(() => [
  {
    value: 'inherit',
    label: t(`common.proxy.inherit.${props.scope}`),
  },
  { value: 'direct', label: t('common.proxy.mode.direct') },
  { value: 'custom', label: t('common.proxy.mode.custom') },
])
const mutation = computed(() => proxyMutation(mode.value, endpoint.value))
const endpointError = computed(() =>
  mode.value === 'custom' && touched.value && mutation.value === undefined
    ? t('common.proxy.invalid')
    : undefined,
)
const displayValue = computed(
  () => props.view.display_url ?? t(`common.proxy.mode.${props.view.effective_mode}`),
)
const sourceLabel = computed(() => t(`common.proxy.source.${props.view.effective_source}`))
const effectiveLabel = computed(() =>
  t('common.proxy.effective', {
    mode: t(`common.proxy.mode.${props.view.effective_mode}`),
  }),
)

function beginEdit(): void {
  if (!props.supported || props.disabled || pending.value) return
  mode.value = props.view.configured_mode
  endpoint.value = ''
  touched.value = false
  saveFailed.value = false
  editing.value = true
}

function cancel(): void {
  if (pending.value) return
  editing.value = false
  endpoint.value = ''
  saveFailed.value = false
}

function updateMode(value: string): void {
  if (!['inherit', 'direct', 'custom'].includes(value)) return
  mode.value = value as ProxyConfiguredMode
  endpoint.value = ''
  touched.value = false
  saveFailed.value = false
}

function updateEndpoint(value: string): void {
  endpoint.value = value
  touched.value = true
  saveFailed.value = false
}

async function save(): Promise<void> {
  touched.value = true
  saveFailed.value = false
  const value = mutation.value
  if (value === undefined || !props.supported || props.disabled || pending.value) return

  pending.value = true
  try {
    await props.saveProxy(value)
    editing.value = false
    endpoint.value = ''
  } catch {
    saveFailed.value = true
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <div
    class="proxy-config-editor"
    :class="{
      'proxy-config-editor--editing': supported && editing,
      'proxy-config-editor--divided': divided,
    }"
  >
    <div class="proxy-config-editor__identity">
      <strong>{{ t('common.proxy.title') }}</strong>
      <StatusBadge
        size="compact"
        :tone="!supported || view.configured_mode === 'inherit' ? 'neutral' : 'info'"
        :icon="!supported ? 'off' : view.configured_mode === 'inherit' ? 'check' : 'edit'"
      >
        {{ supported ? sourceLabel : t('common.proxy.unsupportedBadge') }}
      </StatusBadge>
    </div>

    <template v-if="!supported">
      <div class="proxy-config-editor__value">
        <strong>{{ t('common.proxy.unsupported') }}</strong>
        <small>{{ t('common.proxy.unsupportedHelp') }}</small>
      </div>
    </template>

    <template v-else-if="!editing">
      <div class="proxy-config-editor__value">
        <code v-if="view.display_url">{{ displayValue }}</code>
        <strong v-else>{{ displayValue }}</strong>
        <small>{{ effectiveLabel }}</small>
      </div>
      <AppButton
        variant="secondary"
        tone="action"
        size="compact"
        :disabled="disabled"
        @click="beginEdit"
      >
        {{ t('common.proxy.edit') }}
      </AppButton>
    </template>

    <form v-else class="proxy-config-editor__form" @submit.prevent="save">
      <AppSelect
        :model-value="mode"
        :label="t('common.proxy.modeLabel')"
        :options="modeOptions"
        size="compact"
        :disabled="disabled || pending"
        @update:model-value="updateMode"
      />
      <CompactFieldError v-if="mode === 'custom'" :id="inputId" :error="endpointError">
        <template #default="{ invalid, describedBy }">
          <AppTextInput
            :id="inputId"
            :model-value="endpoint"
            :label="t('common.proxy.urlLabel')"
            :placeholder="t('common.proxy.placeholder')"
            appearance="surface"
            size="compact"
            autocomplete="off"
            :spellcheck="false"
            monospace
            :disabled="disabled || pending"
            :invalid="invalid"
            :described-by="describedBy"
            @update:model-value="updateEndpoint"
          />
        </template>
      </CompactFieldError>
      <span v-else class="proxy-config-editor__mode-help">
        {{ t(`common.proxy.help.${mode}`) }}
      </span>
      <div class="proxy-config-editor__actions">
        <AppButton variant="ghost" size="compact" :disabled="pending" @click="cancel">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          type="submit"
          size="compact"
          :busy="pending"
          :disabled="disabled || mutation === undefined"
        >
          {{ t('common.proxy.save') }}
        </AppButton>
      </div>
      <span v-if="saveFailed" class="proxy-config-editor__error" role="alert">
        {{ t('common.proxy.saveFailed') }}
      </span>
    </form>
  </div>
</template>

<style scoped>
.proxy-config-editor {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(160px, 1fr) minmax(140px, 0.8fr) auto;
  align-items: center;
  gap: var(--space-4);
  padding: 11px 2px;
}

.proxy-config-editor--divided {
  border-bottom: 1px dashed var(--color-border-subtle);
}

.proxy-config-editor__identity,
.proxy-config-editor__value {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
}

.proxy-config-editor__identity {
  justify-items: start;
}

.proxy-config-editor__identity > strong {
  font-size: var(--text-meta);
}

.proxy-config-editor__value code,
.proxy-config-editor__value strong {
  overflow: hidden;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: 520;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.proxy-config-editor__value small,
.proxy-config-editor__mode-help {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.proxy-config-editor__form {
  display: grid;
  min-width: 0;
  grid-column: 2 / -1;
  grid-template-columns: auto minmax(180px, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
}

.proxy-config-editor__mode-help {
  align-self: center;
}

.proxy-config-editor__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.proxy-config-editor__error {
  grid-column: 1 / -1;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
}

@media (max-width: 800px) {
  .proxy-config-editor {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .proxy-config-editor__value {
    grid-column: 1;
    grid-row: 2;
  }

  .proxy-config-editor > :deep(.app-button) {
    grid-column: 2;
    grid-row: 1 / span 2;
    min-height: var(--touch-target);
  }

  .proxy-config-editor__form {
    grid-column: 1 / -1;
    grid-template-columns: minmax(0, 1fr);
  }

  .proxy-config-editor__actions {
    justify-content: flex-end;
  }

  .proxy-config-editor__actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
