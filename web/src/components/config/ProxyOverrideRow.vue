<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProxyConfiguredMode, ProxyViewDto } from '@/api/control/types'
import { proxyDraftState } from '@/app/resources/proxy'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = withDefaults(
  defineProps<{
    scope: 'global' | 'group'
    view: ProxyViewDto
    mode: ProxyConfiguredMode
    endpoint: string
    supported?: boolean
    disabled?: boolean
    divided?: boolean
  }>(),
  { supported: true, disabled: false, divided: true },
)
const emit = defineEmits<{
  'update:mode': [value: ProxyConfiguredMode]
  'update:endpoint': [value: string]
}>()

const { t } = useI18n()
const inputId = `${useId()}-proxy-url`

const modeOptions = computed<SegmentedControlOption[]>(() => [
  {
    value: 'inherit',
    label: t(`common.proxy.inherit.${props.scope}`),
    disabled: props.disabled,
  },
  { value: 'direct', label: t('common.proxy.mode.direct'), disabled: props.disabled },
  { value: 'custom', label: t('common.proxy.mode.custom'), disabled: props.disabled },
])
const state = computed(() => proxyDraftState(props.view, props.mode, props.endpoint))
const sourceLabel = computed(() => t(`common.proxy.source.${props.view.effective_source}`))
const effectiveLabel = computed(() =>
  t('common.proxy.effective', { mode: t(`common.proxy.mode.${props.view.effective_mode}`) }),
)
const endpointPlaceholder = computed(() =>
  props.view.configured_mode === 'custom' && props.view.display_url
    ? props.view.display_url
    : t('common.proxy.placeholder'),
)
const endpointError = computed(() => (state.value.invalid ? t('common.proxy.invalid') : undefined))
const showKeepCurrentHint = computed(
  () => props.view.configured_mode === 'custom' && !state.value.invalid,
)

function setMode(value: string): void {
  if (props.disabled || !['inherit', 'direct', 'custom'].includes(value)) return
  emit('update:mode', value as ProxyConfiguredMode)
}
</script>

<template>
  <div class="proxy-override-row" :class="{ 'proxy-override-row--divided': divided }">
    <div class="proxy-override-row__identity">
      <strong>{{ t('common.proxy.title') }}</strong>
      <StatusBadge
        size="compact"
        :tone="!supported || view.configured_mode === 'inherit' ? 'neutral' : 'info'"
        :icon="!supported ? 'off' : view.configured_mode === 'inherit' ? 'check' : 'edit'"
      >
        {{ supported ? sourceLabel : t('common.proxy.unsupportedBadge') }}
      </StatusBadge>
      <small>{{ supported ? effectiveLabel : t('common.proxy.unsupportedHelp') }}</small>
    </div>

    <div v-if="supported" class="proxy-override-row__control">
      <SegmentedControl
        :model-value="mode"
        :label="t('common.proxy.modeLabel')"
        :options="modeOptions"
        size="compact"
        @update:model-value="setMode"
      />
      <CompactFieldError v-if="mode === 'custom'" :id="inputId" :error="endpointError">
        <template #default="{ invalid, describedBy }">
          <AppTextInput
            :id="inputId"
            :model-value="endpoint"
            :label="t('common.proxy.urlLabel')"
            :placeholder="endpointPlaceholder"
            appearance="surface"
            size="compact"
            autocomplete="off"
            :spellcheck="false"
            monospace
            :disabled="disabled"
            :invalid="invalid"
            :described-by="describedBy"
            @update:model-value="emit('update:endpoint', $event)"
          />
        </template>
      </CompactFieldError>
      <small v-if="mode === 'custom' && showKeepCurrentHint" class="proxy-override-row__hint">
        {{ t('common.proxy.keepCurrentHint') }}
      </small>
    </div>
  </div>
</template>

<style scoped>
.proxy-override-row {
  display: grid;
  gap: var(--space-2);
  padding: 11px 2px;
}

.proxy-override-row--divided {
  border-bottom: 1px dashed var(--color-border-subtle);
}

.proxy-override-row__identity {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.proxy-override-row__identity > strong {
  font-size: var(--text-meta);
}

.proxy-override-row__identity > small {
  flex-basis: 100%;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.proxy-override-row__control {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
}

.proxy-override-row__control > :deep(.compact-field-error) {
  flex: 1 1 220px;
  min-width: 200px;
}

.proxy-override-row__hint {
  flex-basis: 100%;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
</style>
