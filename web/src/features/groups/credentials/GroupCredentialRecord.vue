<script setup lang="ts">
import {
  ChevronDown,
  CircleCheck,
  CircleOff,
  Ellipsis,
  PencilLine,
  RotateCcw,
  Trash2,
} from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialItemDto, ProxyMutation } from '@/api/control/types'
import ProxyConfigEditor from '@/components/config/ProxyConfigEditor.vue'
import ProxyScopeIndicator from '@/components/config/ProxyScopeIndicator.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { formatLocalInstant } from '@/lib/format'

import { presentCredentialFailureCategory } from './credential-failure-presenter'

const props = defineProps<{
  item: CredentialItemDto
  rowIndex: number
  selected: boolean
  busy: boolean
  expanded: boolean
  weightEditorOpen: boolean
  resolveCopyValue: (id: number) => Promise<string>
  saveProxy: (value: ProxyMutation) => Promise<void>
  proxySupported: boolean
}>()
const emit = defineEmits<{
  'update:selected': [selected: boolean]
  'update:expanded': [expanded: boolean]
  'update:weightEditorOpen': [open: boolean]
  'open-weight': [item: CredentialItemDto]
  weight: [payload: { item: CredentialItemDto; value: string }]
  toggle: [item: CredentialItemDto]
  restore: [item: CredentialItemDto]
  remove: [item: CredentialItemDto]
}>()
const { locale, n, t } = useI18n()
const menuOpen = ref(false)
const draftWeightMode = ref<'auto' | 'manual'>('auto')
const draftWeight = ref('50')
const detailId = computed(() => `group-credential-details-${props.item.credential_id}`)
const weightInputId = computed(() => `group-credential-weight-${props.item.credential_id}`)
const isProblem = computed(
  () => props.item.effective_status === 'cooldown' || props.item.effective_status === 'blacklisted',
)
const weightLabel = computed(() =>
  props.item.weight === null
    ? t('group.credentials.none')
    : t(`group.credentials.weight.${props.item.weight_mode}`, { weight: n(props.item.weight) }),
)
const recentLabel = computed(() =>
  props.item.recent_failure_count === 0
    ? t('group.credentials.recentSuccessOnly', { success: n(props.item.recent_success_count) })
    : t('group.credentials.recent', {
        success: n(props.item.recent_success_count),
        failure: n(props.item.recent_failure_count),
      }),
)
const recoveryLabel = computed(() => {
  if (props.item.recovery.mode === 'none') return t('group.credentials.recovery.none')
  if (props.item.recovery.at_ms !== null)
    return t('group.credentials.recovery.at', {
      time: formatLocalInstant(props.item.recovery.at_ms, locale.value),
    })
  return t(`group.credentials.recovery.${props.item.recovery.mode}`)
})
const failureLabel = computed(() =>
  props.item.recent_failure_count === 0
    ? t('group.credentials.none')
    : `${presentCredentialFailureCategory(t, props.item.last_failure_category)}${
        props.item.last_status_code === null ? '' : ` · ${props.item.last_status_code}`
      }`,
)
const weightModeOptions = computed(() => [
  { value: 'auto', label: t('group.credentials.weightEditor.auto'), disabled: props.busy },
  { value: 'manual', label: t('group.credentials.weightEditor.manual'), disabled: props.busy },
])
const manualWeightValid = computed(() => {
  if (draftWeightMode.value === 'auto') return true
  const value = Number(draftWeight.value)
  return Number.isInteger(value) && value >= 1 && value <= 100
})

watch(
  () => props.weightEditorOpen,
  (open) => {
    if (!open) return
    draftWeightMode.value = props.item.weight_mode
    draftWeight.value = String(props.item.weight ?? 50)
  },
)

function saveWeight(): void {
  if (props.busy || !manualWeightValid.value) return
  emit('weight', {
    item: props.item,
    value: draftWeightMode.value === 'auto' ? 'auto' : String(Number(draftWeight.value)),
  })
  emit('update:weightEditorOpen', false)
}

// 权重列的值可点：展开该行并直接进入权重编辑，作为折叠区设置的发现入口。
function openWeightFromColumn(): void {
  if (props.busy || props.item.configured_status === 'disabled') return
  emit('open-weight', props.item)
}

function runMenuAction(action: 'toggle' | 'restore' | 'remove'): void {
  menuOpen.value = false
  if (action === 'toggle') emit('toggle', props.item)
  else if (action === 'restore') emit('restore', props.item)
  else emit('remove', props.item)
}
</script>

<template>
  <article
    class="ledger-record-list__record group-credential-record"
    :class="{ 'group-credential-record--expanded': expanded }"
    role="row"
    :aria-rowindex="rowIndex"
  >
    <div class="group-credential-record__summary" role="presentation">
      <div class="ledger-record-list__cell group-credential-record__select" role="cell">
        <label>
          <span class="sr-only">{{
            t('group.credentials.selectCredential', { mask: item.mask })
          }}</span>
          <input
            type="checkbox"
            :checked="selected"
            :disabled="busy"
            @change="emit('update:selected', ($event.target as HTMLInputElement).checked)"
          />
        </label>
      </div>

      <div class="ledger-record-list__cell group-credential-record__mask" role="cell">
        <span class="group-credential-record__mobile-label">{{
          t('group.credentials.columns.credential')
        }}</span>
        <span class="group-credential-record__credential">
          <CopyChip
            :value="item.mask"
            :label="t('group.credentials.copy')"
            :success-label="t('common.copied')"
            :failure-label="t('common.copyFailed')"
            :resolve-value="() => resolveCopyValue(item.credential_id)"
          />
          <ProxyScopeIndicator v-if="proxySupported" :view="item.proxy" />
        </span>
      </div>

      <div class="ledger-record-list__cell group-credential-record__status" role="cell">
        <span class="group-credential-record__mobile-label">{{
          t('group.credentials.columns.status')
        }}</span>
        <StatusBadge :status="item.effective_status" size="compact">
          {{ t(`group.credentials.effective.${item.effective_status}`) }}
        </StatusBadge>
      </div>

      <div class="ledger-record-list__cell group-credential-record__weight" role="cell">
        <span class="group-credential-record__mobile-label">{{
          t('group.credentials.columns.weight')
        }}</span>
        <AppTooltip v-if="item.weight === null" :content="t('group.credentials.notScheduledHelp')">
          <span class="group-credential-record__weight-none" tabindex="0">{{ weightLabel }}</span>
        </AppTooltip>
        <AppTooltip v-else :content="t('group.credentials.editWeightHint')">
          <button
            type="button"
            class="group-credential-record__weight-value"
            :disabled="busy || item.configured_status === 'disabled'"
            @click="openWeightFromColumn"
          >
            {{ weightLabel }}
          </button>
        </AppTooltip>
      </div>

      <div class="ledger-record-list__cell group-credential-record__recent" role="cell">
        <span class="group-credential-record__mobile-label">{{
          t('group.credentials.columns.recent')
        }}</span>
        <span>{{ recentLabel }}</span>
      </div>

      <div class="ledger-record-list__cell group-credential-record__actions" role="cell">
        <AppPopover
          v-model:open="menuOpen"
          align="end"
          content-class="app-popover__content--credential-menu"
        >
          <template #trigger>
            <IconButton
              variant="ghost"
              size="compact"
              :label="t('group.credentials.moreActions')"
              :disabled="busy"
            >
              <Ellipsis :size="16" aria-hidden="true" />
            </IconButton>
          </template>
          <div class="group-credential-record__menu">
            <button type="button" :disabled="busy" @click="runMenuAction('toggle')">
              <CircleOff v-if="item.configured_status === 'active'" :size="15" aria-hidden="true" />
              <CircleCheck v-else :size="15" aria-hidden="true" />
              {{
                item.configured_status === 'active'
                  ? t('group.credentials.disable')
                  : t('group.credentials.enable')
              }}
            </button>
            <button
              v-if="isProblem"
              type="button"
              :disabled="busy"
              @click="runMenuAction('restore')"
            >
              <RotateCcw :size="15" aria-hidden="true" />{{ t('group.credentials.restore') }}
            </button>
            <div class="group-credential-record__menu-divider"></div>
            <button
              type="button"
              class="group-credential-record__menu-danger"
              :disabled="busy"
              @click="runMenuAction('remove')"
            >
              <Trash2 :size="15" aria-hidden="true" />{{ t('group.credentials.delete') }}
            </button>
          </div>
        </AppPopover>
        <IconButton
          class="group-credential-record__toggle"
          variant="ghost"
          size="compact"
          :label="expanded ? t('group.credentials.collapse') : t('group.credentials.expand')"
          :aria-expanded="expanded"
          :aria-controls="detailId"
          @click="emit('update:expanded', !expanded)"
        >
          <ChevronDown :size="16" aria-hidden="true" />
        </IconButton>
      </div>
    </div>

    <div
      class="group-credential-record__disclosure"
      :class="{ 'group-credential-record__disclosure--expanded': expanded }"
      role="presentation"
    >
      <div class="group-credential-record__disclosure-inner" role="presentation">
        <div
          :id="detailId"
          class="ledger-record-list__cell group-credential-record__details"
          role="cell"
          :aria-hidden="!expanded"
          :inert="!expanded || undefined"
        >
          <div class="group-credential-record__settings">
            <div class="group-credential-record__block">
              <span class="group-credential-record__block-title">
                {{ t('group.credentials.columns.weight') }}
              </span>
              <div class="group-credential-record__panel">
                <template v-if="!weightEditorOpen">
                  <span class="group-credential-record__mode-tag">
                    {{ t(`group.credentials.weightEditor.${item.weight_mode}`) }}
                  </span>
                  <span class="group-credential-record__panel-value">
                    {{ item.weight === null ? t('group.credentials.none') : n(item.weight) }}
                  </span>
                  <IconButton
                    class="group-credential-record__panel-edit"
                    variant="ghost"
                    tone="action"
                    size="xs"
                    :label="t('group.credentials.editWeight')"
                    :disabled="busy || item.configured_status === 'disabled'"
                    @click="emit('update:weightEditorOpen', true)"
                  >
                    <PencilLine :size="12" aria-hidden="true" />
                  </IconButton>
                </template>
                <form
                  v-else
                  class="group-credential-record__weight-form"
                  @submit.prevent="saveWeight"
                >
                  <SegmentedControl
                    v-model="draftWeightMode"
                    :label="t('group.credentials.weightEditor.mode')"
                    :options="weightModeOptions"
                    size="compact"
                  />
                  <label class="sr-only" :for="weightInputId">
                    {{ t('group.credentials.weightEditor.value') }}
                  </label>
                  <input
                    :id="weightInputId"
                    v-model="draftWeight"
                    :class="{ 'is-concealed': draftWeightMode === 'auto' }"
                    type="number"
                    min="1"
                    max="100"
                    step="1"
                    inputmode="numeric"
                    :disabled="busy || draftWeightMode === 'auto'"
                    :tabindex="draftWeightMode === 'auto' ? -1 : undefined"
                    :aria-hidden="draftWeightMode === 'auto' ? 'true' : undefined"
                    :aria-invalid="!manualWeightValid || undefined"
                  />
                  <div class="group-credential-record__panel-actions">
                    <AppButton
                      variant="ghost"
                      size="compact"
                      @click="emit('update:weightEditorOpen', false)"
                    >
                      {{ t('group.credentials.weightEditor.cancel') }}
                    </AppButton>
                    <AppButton type="submit" size="compact" :disabled="busy || !manualWeightValid">
                      {{ t('group.credentials.weightEditor.save') }}
                    </AppButton>
                  </div>
                  <p
                    v-if="draftWeightMode === 'manual' && !manualWeightValid"
                    class="group-credential-record__panel-error"
                    role="alert"
                  >
                    {{ t('group.credentials.weightEditor.invalid') }}
                  </p>
                </form>
              </div>
            </div>

            <ProxyConfigEditor
              class="group-credential-record__proxy"
              appearance="card"
              :view="item.proxy"
              :save-proxy="saveProxy"
              :supported="proxySupported"
              :disabled="busy"
              :divided="false"
            />
          </div>

          <div class="group-credential-record__block">
            <span class="group-credential-record__block-title">
              {{ t('group.credentials.diagnostics') }}
            </span>
            <dl class="group-credential-record__runtime-details">
              <div>
                <dt>{{ t('group.credentials.detailsFailure') }}</dt>
                <dd>{{ failureLabel }}</dd>
              </div>
              <div>
                <dt>{{ t('group.credentials.detailsRecovery') }}</dt>
                <dd>{{ recoveryLabel }}</dd>
              </div>
              <div>
                <dt>{{ t('group.credentials.detailsConsecutive') }}</dt>
                <dd>{{ n(item.consecutive_failure_count) }}</dd>
              </div>
            </dl>
          </div>
        </div>
      </div>
    </div>
  </article>
</template>

<style scoped>
.group-credential-record {
  align-items: stretch;
  padding: 0;
}

.group-credential-record__summary {
  position: relative;
  display: grid;
  min-height: 52px;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: center;
  padding: 8px 0;
}

.group-credential-record__select {
  display: flex;
  justify-content: center;
}

.group-credential-record__select label {
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  cursor: pointer;
}
.group-credential-record__select input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}

.group-credential-record__mask,
.group-credential-record__weight,
.group-credential-record__recent {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.group-credential-record__credential {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
}

.group-credential-record__weight-none {
  color: var(--color-text-faint);
  text-decoration: underline dotted;
  text-underline-offset: 3px;
}

.group-credential-record__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-1);
}

.group-credential-record__toggle svg {
  transition: transform var(--duration-normal) var(--easing-standard);
}

.group-credential-record__toggle[aria-expanded='true'] svg {
  transform: rotate(180deg);
}

/*
 * 背景沿用账号卡片展开区（subscription-account__detail）的取值。取消上边框后
 * 只剩底色一条区分线索，因此左右内缩并加圆角，用“比列表窄一圈的嵌套块”表达
 * 层级归属；内缩必须配圆角，否则方角内缩看起来像渲染错位。
 */
.group-credential-record__details {
  display: grid;
  gap: 13px;
  min-width: 0;
  border-radius: var(--radius-control);
  background: color-mix(in srgb, var(--color-action-soft) 72%, var(--color-surface));
  margin: 0 14px 6px;
  padding: 13px 14px 14px;
}

.group-credential-record__disclosure {
  display: grid;
  grid-column: 1 / -1;
  grid-template-rows: 0fr;
  transition: grid-template-rows var(--duration-normal) var(--easing-standard);
}

.group-credential-record__disclosure--expanded {
  grid-template-rows: 1fr;
}

.group-credential-record__disclosure-inner {
  min-height: 0;
  overflow: hidden;
}

/* 与上方权重／出站代理的面板同款：标题在白底外，内容落在白底面板里。 */
.group-credential-record__details dl {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-5);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  margin: 0;
  padding: 7px 9px;
}

/* card 外观自带标题与白底面板，虚线分隔反而多余；宽度由 __settings 的列约束。 */
.group-credential-record__proxy {
  min-width: 0;
}

.group-credential-record__details dl div {
  min-width: 0;
}

.group-credential-record__details dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.group-credential-record__details dd {
  margin: 3px 0 0;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 560;
}

.group-credential-record__mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

/*
 * 折叠区里的设置块与 ProxyConfigEditor 的 card 外观保持同款：标题在白底外、
 * 白底面板承载值与编辑控件。这里是有意复刻那套视觉，若之后再多一个设置项，
 * 应把这套外壳抽成共享组件而不是继续复制。
 */
/*
 * 权重与出站代理并排成两列，诊断在下面占满整行。这里不设 max-width：
 * 否则设置行的右边缘会比下方诊断面板短一截，两行对不齐。
 */
.group-credential-record__settings {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: start;
  gap: 13px 16px;
}

.group-credential-record__block {
  display: grid;
  min-width: 0;
  gap: 6px;
}

.group-credential-record__block-title {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 680;
  letter-spacing: 0.06em;
}

.group-credential-record__panel {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 8px;
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 7px 9px;
}

.group-credential-record__mode-tag {
  flex: none;
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 2px 7px;
  font-size: var(--text-label-xs);
  font-weight: 620;
}

.group-credential-record__panel-value {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
}

.group-credential-record__panel-edit {
  width: 22px;
  min-height: 22px;
  height: 22px;
  flex: none;
  margin-left: 2px;
}

.group-credential-record__weight-form {
  display: flex;
  flex: 1 1 100%;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.group-credential-record__weight-form > input {
  width: 64px;
  min-height: 26px;
  flex: none;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 6px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}

.group-credential-record__weight-form > input.is-concealed {
  visibility: hidden;
  pointer-events: none;
}

.group-credential-record__panel-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 2px;
  margin-left: auto;
}

.group-credential-record__panel-actions :deep(.app-button) {
  min-height: 26px;
  padding-inline: 8px;
  font-size: var(--text-label-xs);
}

.group-credential-record__panel-error {
  flex: 1 1 100%;
  margin: 0;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
}

.group-credential-record__weight-value {
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--color-text);
  padding: 2px 4px;
  font: inherit;
  cursor: pointer;
  text-decoration: underline dotted;
  text-decoration-color: var(--color-border-control);
  text-underline-offset: 3px;
}

.group-credential-record__weight-value:hover:not(:disabled) {
  background: var(--color-interactive-hover);
  text-decoration-color: var(--color-text-faint);
}

.group-credential-record__weight-value:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}

.group-credential-record__weight-value:disabled {
  color: var(--color-text-faint);
  cursor: not-allowed;
  text-decoration: none;
}

.group-credential-record__menu {
  display: grid;
  width: 100%;
  gap: 1px;
}

.group-credential-record__menu button {
  display: flex;
  width: 100%;
  align-items: center;
  gap: var(--space-2);
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text);
  padding: 7px 6px;
  font: inherit;
  font-size: var(--text-button);
  text-align: left;
  cursor: pointer;
}

.group-credential-record__menu button svg {
  flex: none;
  color: var(--color-text-faint);
}

.group-credential-record__menu button:hover:not(:disabled) {
  background: var(--color-surface-sunken);
}

.group-credential-record__menu button:hover:not(:disabled) svg {
  color: var(--color-text-muted);
}

.group-credential-record__menu button:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: -2px;
}

.group-credential-record__menu button:disabled {
  cursor: not-allowed;
  opacity: 0.46;
}

.group-credential-record__menu-divider {
  height: 1px;
  margin: 4px -8px;
  background: var(--color-border-subtle);
}

.group-credential-record__menu button.group-credential-record__menu-danger,
.group-credential-record__menu button.group-credential-record__menu-danger svg {
  color: var(--color-danger);
}

.group-credential-record__menu button.group-credential-record__menu-danger:hover:not(:disabled) {
  background: var(--color-danger-bg);
}

:global(.app-popover__content.app-popover__content--credential-menu) {
  width: auto;
  min-width: 176px;
  border-color: var(--color-border-control);
  border-radius: 10px;
  padding: 8px;
}

@media (max-width: 860px) {
  .group-credential-record {
    padding: 0;
  }

  .group-credential-record__summary {
    grid-template-columns: var(--ledger-record-list-card-grid, minmax(0, 0.48fr) minmax(0, 1.52fr));
    align-items: start;
    gap: 14px 16px;
    padding: 16px 58px 16px 16px;
  }

  .group-credential-record__select {
    position: absolute;
    top: 2px;
    right: 4px;
  }

  .group-credential-record__select label {
    width: var(--touch-target);
    height: var(--touch-target);
  }

  .group-credential-record__mask,
  .group-credential-record__recent,
  .group-credential-record__actions {
    grid-column: 1 / -1;
  }

  .group-credential-record__mask,
  .group-credential-record__status,
  .group-credential-record__weight,
  .group-credential-record__recent {
    display: grid;
    align-content: start;
    gap: 5px;
  }

  .group-credential-record__recent,
  .group-credential-record__actions {
    border-top: 1px solid var(--color-border-subtle);
    padding-top: 11px;
  }

  .group-credential-record__actions {
    justify-content: flex-start;
  }

  .group-credential-record__actions :deep(.app-button),
  .group-credential-record__actions :deep(.icon-button) {
    min-height: var(--touch-target);
  }

  .group-credential-record__details {
    margin: 0 8px 4px;
    padding: 12px;
  }

  .group-credential-record__settings,
  .group-credential-record__details dl {
    grid-template-columns: 1fr;
  }

  .group-credential-record__mobile-label {
    display: inline;
  }
}

@media (max-width: 560px) {
  .group-credential-record__summary {
    padding: 14px 58px 14px 13px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .group-credential-record__toggle svg,
  .group-credential-record__disclosure {
    transition: none;
  }
}
</style>
