<script setup lang="ts">
import { PencilLine } from '@lucide/vue'
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProxyConfiguredMode, ProxyMutation, ProxyViewDto } from '@/api/control/types'
import { proxyDraftState, proxyPlaceholderURL } from '@/app/resources/proxy'
import AppButton from '@/components/ui/AppButton.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import IconButton from '@/components/ui/IconButton.vue'

const props = withDefaults(
  defineProps<{
    view: ProxyViewDto
    saveProxy: (value: ProxyMutation) => Promise<void>
    supported?: boolean
    disabled?: boolean
  }>(),
  { supported: true, disabled: false },
)

const { t } = useI18n()
const inputId = `${useId()}-proxy-url`
const editing = ref(false)
const pending = ref(false)
const mode = ref<ProxyConfiguredMode>('inherit')
const endpoint = ref('')
const touched = ref(false)
const saveFailed = ref(false)

const modeOptions = computed(() => [
  { value: 'inherit', label: t('common.proxy.mode.inherit') },
  { value: 'direct', label: t('common.proxy.mode.direct') },
  { value: 'custom', label: t('common.proxy.mode.custom') },
])
// 分段控件没有整体 disabled，逐项禁用。
const segmentedModeOptions = computed(() =>
  modeOptions.value.map((option) => ({ ...option, disabled: props.disabled || pending.value })),
)
const draft = computed(() => proxyDraftState(props.view, mode.value, endpoint.value))
const endpointError = computed(() =>
  mode.value === 'custom' && touched.value && draft.value.invalid
    ? t('common.proxy.invalid')
    : undefined,
)
const configuredModeLabel = computed(() => t(`common.proxy.mode.${props.view.configured_mode}`))
const effectiveValue = computed(
  () => props.view.display_url ?? t(`common.proxy.mode.${props.view.effective_mode}`),
)
// 显式选了直连时，模式标签本身就是结论，再跟一个“直连”属于重复。
const showValue = computed(() => props.view.configured_mode !== 'direct')

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

// 供卡片徽章行的指示器一次完成“展开 + 进入编辑”。
defineExpose({ beginEdit })

async function save(): Promise<void> {
  touched.value = true
  saveFailed.value = false
  const value = draft.value.value
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
  <div class="setting-panel proxy-config-editor">
    <span class="setting-panel__title">{{ t('common.proxy.title') }}</span>

    <div class="setting-panel__body">
      <div v-if="!supported" class="proxy-config-editor__unsupported">
        <strong>{{ t('common.proxy.unsupported') }}</strong>
        <small>{{ t('common.proxy.unsupportedHelp') }}</small>
      </div>

      <template v-else-if="!editing">
        <span class="setting-panel__tag">{{ configuredModeLabel }}</span>
        <code v-if="showValue" class="setting-panel__value proxy-config-editor__value">
          {{ effectiveValue }}
        </code>
        <IconButton
          class="setting-panel__edit"
          variant="ghost"
          tone="action"
          size="xs"
          :label="t('common.proxy.edit')"
          :disabled="disabled"
          @click="beginEdit"
        >
          <PencilLine :size="12" aria-hidden="true" />
        </IconButton>
      </template>

      <form v-else class="setting-panel__form" @submit.prevent="save">
        <SegmentedControl
          class="proxy-config-editor__mode"
          :model-value="mode"
          :label="t('common.proxy.modeLabel')"
          :options="segmentedModeOptions"
          size="xs"
          @update:model-value="updateMode"
        />
        <CompactFieldError
          v-if="mode === 'custom'"
          :id="inputId"
          class="proxy-config-editor__endpoint"
          :error="endpointError"
        >
          <template #default="{ invalid, describedBy }">
            <AppTextInput
              :id="inputId"
              :model-value="endpoint"
              :label="t('common.proxy.urlLabel')"
              :placeholder="proxyPlaceholderURL(view) ?? t('common.proxy.placeholder')"
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
        <div class="setting-panel__actions">
          <AppButton variant="ghost" size="compact" :disabled="pending" @click="cancel">
            {{ t('common.cancel') }}
          </AppButton>
          <AppButton
            type="submit"
            size="compact"
            :busy="pending"
            :disabled="disabled || !draft.dirty || draft.invalid"
          >
            {{ t('common.proxy.save') }}
          </AppButton>
        </div>
        <span v-if="saveFailed" class="setting-panel__error" role="alert">
          {{ t('common.proxy.saveFailed') }}
        </span>
      </form>
    </div>
  </div>
</template>

<style scoped>
/* 面板外壳由全局 .setting-panel 提供，这里只写代理特有的部分。 */
.proxy-config-editor__value {
  overflow: hidden;
  color: var(--color-text);
  font-weight: 520;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 渠道不支持时没有编辑入口，说明文案占满整行并允许换行。 */
.proxy-config-editor__unsupported {
  display: flex;
  min-width: 0;
  flex: 1 1 100%;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 6px;
}

.proxy-config-editor__unsupported > strong {
  font-size: var(--text-label-xs);
}

.proxy-config-editor__unsupported > small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.proxy-config-editor__mode {
  flex: none;
}

.proxy-config-editor__endpoint {
  /* 默认给错误图标预留 38px，在这个窄面板里会把占位符挤掉，按缩小后的图标重算。 */
  --compact-field-error-indicator-size: 22px;
  --compact-field-error-indicator-right: 2px;
  --compact-field-error-input-gap: 4px;
  /* basis 归零，地址栏独吞剩余宽度，控件行不折行。 */
  flex: 1 1 0;
  min-width: 0;
}

.proxy-config-editor__endpoint :deep(.app-text-input) {
  min-height: 26px;
  font-size: var(--text-label-xs);
}
</style>
