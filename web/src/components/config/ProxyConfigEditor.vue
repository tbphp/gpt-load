<script setup lang="ts">
import { PencilLine } from '@lucide/vue'
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProxyConfiguredMode, ProxyMutation, ProxyViewDto } from '@/api/control/types'
import { proxyMutation } from '@/app/resources/proxy'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = withDefaults(
  defineProps<{
    view: ProxyViewDto
    saveProxy: (value: ProxyMutation) => Promise<void>
    supported?: boolean
    disabled?: boolean
    divided?: boolean
    appearance?: 'row' | 'card'
  }>(),
  { supported: true, disabled: false, divided: true, appearance: 'row' },
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
  { value: 'inherit', label: t('common.proxy.mode.inherit') },
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
const configuredModeLabel = computed(() => t(`common.proxy.mode.${props.view.configured_mode}`))
// 显式选了直连时，标签本身就是结论，再跟一个“直连”属于重复。
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
      'proxy-config-editor--card': appearance === 'card',
    }"
  >
    <div class="proxy-config-editor__identity">
      <strong>{{ t('common.proxy.title') }}</strong>
      <StatusBadge
        v-if="appearance !== 'card'"
        size="compact"
        :tone="!supported || view.configured_mode === 'inherit' ? 'neutral' : 'info'"
        :icon="!supported ? 'off' : view.configured_mode === 'inherit' ? 'check' : 'edit'"
      >
        {{ supported ? sourceLabel : t('common.proxy.unsupportedBadge') }}
      </StatusBadge>
    </div>

    <div class="proxy-config-editor__body">
      <template v-if="!supported">
        <div class="proxy-config-editor__value">
          <strong>{{ t('common.proxy.unsupported') }}</strong>
          <small>{{ t('common.proxy.unsupportedHelp') }}</small>
        </div>
      </template>

      <template v-else-if="!editing">
        <span v-if="appearance === 'card'" class="proxy-config-editor__mode-tag">
          {{ configuredModeLabel }}
        </span>
        <div v-if="appearance !== 'card' || showValue" class="proxy-config-editor__value">
          <code v-if="view.display_url">{{ displayValue }}</code>
          <strong v-else>{{ displayValue }}</strong>
          <small>{{ effectiveLabel }}</small>
        </div>
        <IconButton
          v-if="appearance === 'card'"
          class="proxy-config-editor__edit"
          variant="ghost"
          tone="action"
          size="xs"
          :label="t('common.proxy.edit')"
          :disabled="disabled"
          @click="beginEdit"
        >
          <PencilLine :size="12" aria-hidden="true" />
        </IconButton>
        <AppButton
          v-else
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
  </div>
</template>

<style scoped>
/*
 * 这里用自适应换行（而非按视口宽度切换的断点）：账号卡片、密钥展开行等嵌入场景
 * 本身就比设置页整栏窄得多，视口宽度并不能反映这个组件实际可用的空间。
 */
.proxy-config-editor {
  display: flex;
  flex-wrap: wrap;
  min-width: 0;
  align-items: center;
  gap: var(--space-2) var(--space-4);
  padding: 11px 2px;
}

.proxy-config-editor--divided {
  border-bottom: 1px dashed var(--color-border-subtle);
}

/* row 外观下 body 不参与布局，子元素直接作为根的 flex 项，排布与重构前一致。 */
.proxy-config-editor__body {
  display: contents;
}

.proxy-config-editor__identity,
.proxy-config-editor__value {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
}

.proxy-config-editor__identity {
  flex: 1 1 150px;
  justify-items: start;
}

.proxy-config-editor__identity > strong {
  font-size: var(--text-meta);
}

.proxy-config-editor__value {
  flex: 2 1 140px;
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

.proxy-config-editor__body > :deep(.app-button) {
  flex: none;
  margin-left: auto;
}

.proxy-config-editor__form {
  display: flex;
  flex: 1 1 100%;
  flex-wrap: wrap;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.proxy-config-editor__form > :deep(.app-select__trigger) {
  flex: 1 1 128px;
}

.proxy-config-editor__form > :deep(.compact-field-error) {
  flex: 2 1 180px;
  min-width: 160px;
}

.proxy-config-editor__mode-help {
  flex: 1 1 100%;
  align-self: center;
}

.proxy-config-editor__actions {
  display: flex;
  flex: 1 1 100%;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-1);
}

.proxy-config-editor__error {
  flex: 1 1 100%;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
}

/* ── card 外观 ──
 * 标题作为独立板块标题留在白底之外（与「概览」等小节同级），白底面板只承载
 * 具体代理信息与编辑控件；字号统一对齐概览表的 --text-label-xs。
 */
.proxy-config-editor--card {
  display: grid;
  gap: 6px;
  padding: 0;
}

.proxy-config-editor--card.proxy-config-editor--divided {
  border-bottom: 0;
}

/* 基础规则把 identity / value 设为 grid，横向排布必须显式改回 flex 才生效。 */
.proxy-config-editor--card .proxy-config-editor__identity {
  display: flex;
  flex: none;
  align-items: center;
  gap: 6px;
}

.proxy-config-editor--card .proxy-config-editor__identity > strong {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 680;
  letter-spacing: 0.06em;
}

.proxy-config-editor--card .proxy-config-editor__body {
  display: flex;
  flex-wrap: wrap;
  min-width: 0;
  align-items: center;
  gap: 6px 8px;
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 7px 9px;
}

.proxy-config-editor--card .proxy-config-editor__mode-tag {
  flex: none;
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 2px 7px;
  font-size: var(--text-label-xs);
  font-weight: 620;
}

.proxy-config-editor--card .proxy-config-editor__value {
  display: flex;
  flex: 0 1 auto;
  min-width: 0;
  align-items: center;
  gap: 6px;
}

.proxy-config-editor--card .proxy-config-editor__value code,
.proxy-config-editor--card .proxy-config-editor__value strong {
  font-size: var(--text-label-xs);
}

/* 旁边还有编辑按钮时，“当前生效”与值本身重复。 */
.proxy-config-editor--card .proxy-config-editor__value:has(+ *) small {
  display: none;
}

/* 不支持的渠道没有后续元素，说明文案要留下并允许换行。 */
.proxy-config-editor--card .proxy-config-editor__value:not(:has(+ *)) {
  align-items: baseline;
  flex-wrap: wrap;
}

/* 编辑入口跟着左侧内容走，不靠右吸边；尺寸压到不高于同行的模式标签。 */
.proxy-config-editor--card .proxy-config-editor__edit {
  width: 22px;
  min-height: 22px;
  height: 22px;
  flex: none;
  margin-left: 2px;
}

/* 编辑态压进同一行：下拉固定宽度，不随所选模式伸缩；控件高度同步收小。 */
.proxy-config-editor--card .proxy-config-editor__form {
  flex: 1 1 100%;
  gap: 6px;
}

.proxy-config-editor--card .proxy-config-editor__form > :deep(.app-select__trigger) {
  width: 92px;
  min-height: 26px;
  flex: none;
  padding-inline: 7px;
  font-size: var(--text-label-xs);
}

.proxy-config-editor--card .proxy-config-editor__form > :deep(.compact-field-error) {
  /* 默认给错误图标预留 38px，在这个窄面板里会把占位符挤掉，按缩小后的图标重算。 */
  --compact-field-error-indicator-size: 22px;
  --compact-field-error-indicator-right: 2px;
  --compact-field-error-input-gap: 4px;
  flex: 1 1 120px;
  min-width: 0;
}

.proxy-config-editor--card .proxy-config-editor__form :deep(.app-text-input) {
  min-height: 26px;
  font-size: var(--text-label-xs);
}

.proxy-config-editor--card .proxy-config-editor__mode-help {
  display: none;
}

.proxy-config-editor--card .proxy-config-editor__actions {
  flex: none;
  margin-left: auto;
  gap: 2px;
}

.proxy-config-editor--card .proxy-config-editor__actions :deep(.app-button) {
  min-height: 26px;
  padding-inline: 8px;
  font-size: var(--text-label-xs);
}

@media (max-width: 860px) {
  .proxy-config-editor:not(.proxy-config-editor--card)
    .proxy-config-editor__actions
    :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
