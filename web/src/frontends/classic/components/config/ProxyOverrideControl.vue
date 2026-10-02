<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProxyConfiguredMode, ProxyViewDto } from '@/api/control/types'
import { proxyDraftState } from '@/app/resources/proxy'
import ProxySelect from '@/features/proxies/ProxySelect.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'

const props = withDefaults(
  defineProps<{
    /** 已保存的基线，用于推导占位地址与校验状态。 */
    base: ProxyViewDto
    mode: ProxyConfiguredMode
    endpoint: string
    disabled?: boolean
  }>(),
  { disabled: false },
)
const emit = defineEmits<{
  'update:mode': [value: ProxyConfiguredMode]
  'update:endpoint': [value: string]
}>()

const { t } = useI18n()
const inputId = `${useId()}-proxy-url`

// inherit 由外层「覆盖 / 恢复默认」开关表达，这里只给出显式覆盖的两种取值。
const modeOptions = computed<SegmentedControlOption[]>(() => [
  { value: 'direct', label: t('common.proxy.mode.direct'), disabled: props.disabled },
  { value: 'custom', label: t('proxies.select'), disabled: props.disabled },
])
const error = computed(() =>
  proxyDraftState(props.base, props.mode, props.endpoint).invalid
    ? t('proxies.selectHelp')
    : undefined,
)

function setMode(value: string): void {
  if (props.disabled || (value !== 'direct' && value !== 'custom')) return
  emit('update:mode', value)
}
</script>

<template>
  <div class="proxy-override-control">
    <SegmentedControl
      class="proxy-override-control__mode"
      :model-value="mode === 'custom' ? 'custom' : 'direct'"
      :label="t('common.proxy.modeLabel')"
      :options="modeOptions"
      size="xs"
      @update:model-value="setMode"
    />

    <CompactFieldError v-if="mode === 'custom'" :id="inputId" :error="error">
      <template #default="{ invalid, describedBy }">
        <ProxySelect
          :id="inputId"
          :model-value="endpoint"
          :saved-id="base.proxy_id"
          :saved-name="base.proxy_name"
          :saved-address="base.display_url"
          :reference-state="base.reference_state"
          :disabled="disabled"
          :invalid="invalid"
          :described-by="describedBy"
          @update:model-value="emit('update:endpoint', $event)"
        />
      </template>
    </CompactFieldError>
  </div>
</template>

<style scoped>
.proxy-override-control {
  display: flex;
  width: 520px;
  max-width: 100%;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.proxy-override-control__mode {
  flex: none;
}

.proxy-override-control > :deep(.compact-field-error) {
  flex: 1;
  min-width: 0;
}

@media (max-width: 560px) {
  .proxy-override-control {
    flex-wrap: wrap;
  }

  .proxy-override-control > :deep(.compact-field-error) {
    flex-basis: 100%;
  }
}
</style>
