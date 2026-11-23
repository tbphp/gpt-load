<script setup lang="ts">
import { ArrowUpNarrowWide, Check, Network, Scale, Unplug, X } from '@lucide/vue'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import {
  AppBadge,
  AppButton,
  AppIcon,
  AppIconButton,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import { isValidGroupPriority } from '@shared/group-priority'

const props = withDefaults(
  defineProps<{
    row: CredentialRow
    weight?: boolean
    disabled?: boolean
    savePriority?: (value: number) => Promise<void>
  }>(),
  { weight: true, savePriority: undefined },
)
const emit = defineEmits<{ dirty: [value: boolean] }>()
const { t, n } = useI18n()
const editing = ref(false)
const draft = ref('')
const pending = ref(false)
const attempted = ref(false)
const input = ref<InstanceType<typeof AppTextField>>()
// 校验与凭据详情面板保持一致。
const invalid = computed(() => !isValidGroupPriority(draft.value))
const dirty = computed(() => editing.value && draft.value.trim() !== String(props.row.priority))
const priorityLabel = computed(() => `${t('credentialCards.priority')} ${n(props.row.priority)}`)
const priorityHint = computed(() =>
  [
    t('credentialCards.credentialPriority', { value: n(props.row.priority) }),
    t('credentialCards.priorityHelp'),
  ].join('\n'),
)
watch(dirty, (value) => emit('dirty', value))
onScopeDispose(() => emit('dirty', false))
const ownProxy = computed(
  () =>
    props.row.proxy.mode !== 'inherit' &&
    props.row.proxy.referenceState !== 'disabled' &&
    props.row.proxy.referenceState !== 'deleted',
)
const proxyLabel = computed(() =>
  [
    t('credentialCards.proxyOverride', {
      mode: t('credentialCards.proxyMode.' + props.row.proxy.mode),
    }),
    props.row.proxy.mode === 'custom' ? props.row.proxy.display : '',
  ]
    .filter(Boolean)
    .join('\n'),
)
async function edit(): Promise<void> {
  if (!props.savePriority || props.disabled || pending.value || editing.value) return
  draft.value = String(props.row.priority)
  attempted.value = false
  editing.value = true
  await nextTick()
  input.value?.focus()
  input.value?.select()
}
function cancel(): void {
  if (pending.value) return
  editing.value = false
  attempted.value = false
}
async function save(): Promise<void> {
  if (!props.savePriority || !editing.value || pending.value) return
  if (!dirty.value) {
    cancel()
    return
  }
  attempted.value = true
  if (invalid.value) return
  pending.value = true
  try {
    await props.savePriority(Number(draft.value))
    editing.value = false
    attempted.value = false
  } catch {
    // 失败提示由列表统一发出；保留输入以便重试。
  } finally {
    pending.value = false
  }
}
function enter(event: KeyboardEvent): void {
  if (event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  void save()
}
function blur(event: FocusEvent): void {
  // 焦点移到编辑器自身的保存/取消按钮时不提交，交给按钮处理。
  const next = event.relatedTarget
  if (next instanceof Node && (event.currentTarget as Element | null)?.contains(next)) return
  void save()
}
</script>

<template>
  <span class="modern-credential-routing-meta">
    <span
      v-if="editing"
      class="modern-credential-priority-editor"
      @keydown.esc.stop.prevent="cancel"
      @focusout="blur"
    >
      <AppTooltip :label="attempted && invalid ? t('groups.edit.priorityError') : undefined">
        <AppTextField
          ref="input"
          v-model="draft"
          class="modern-credential-priority-input"
          :label="t('credentialCards.priority')"
          label-hidden
          inputmode="numeric"
          size="xs"
          :disabled="pending"
          :invalid="attempted && invalid"
          @keydown.enter.stop="enter"
        >
          <template #suffix>
            <span class="modern-credential-priority-actions">
              <AppIconButton
                :icon="Check"
                :label="t('ui.save')"
                size="xxs"
                variant="ghost"
                :loading="pending"
                :disabled="pending"
                @pointerdown.prevent
                @click="save"
              />
              <AppIconButton
                :icon="X"
                :label="t('ui.cancel')"
                size="xxs"
                variant="ghost"
                :disabled="pending"
                @pointerdown.prevent
                @click="cancel"
              />
            </span>
          </template>
        </AppTextField>
      </AppTooltip>
    </span>
    <AppTooltip v-else :label="priorityHint">
      <AppButton
        v-if="savePriority"
        class="modern-credential-priority-chip"
        variant="text"
        size="xxs"
        :disabled="disabled"
        :aria-label="`${priorityLabel} · ${t('ui.edit')}`"
        @click="edit"
      >
        <AppBadge :icon="ArrowUpNarrowWide" size="xs">{{ priorityLabel }}</AppBadge>
      </AppButton>
      <span v-else class="modern-credential-priority-chip" tabindex="0">
        <AppBadge :icon="ArrowUpNarrowWide" size="xs">{{ priorityLabel }}</AppBadge>
      </span>
    </AppTooltip>
    <AppTooltip
      v-if="weight && row.weightManual != null"
      :label="t('credentialCards.manualWeight', { value: n(row.weightManual) })"
    >
      <span class="modern-credential-routing-item" tabindex="0">
        <AppIcon :icon="Scale" size="xs" />{{ n(row.weightManual) }}
      </span>
    </AppTooltip>
    <AppTooltip v-if="ownProxy" :label="proxyLabel">
      <span class="modern-credential-routing-item" tabindex="0">
        <AppIcon :icon="row.proxy.mode === 'direct' ? Unplug : Network" size="sm" />
      </span>
    </AppTooltip>
  </span>
</template>

<style scoped>
.modern-credential-routing-meta,
.modern-credential-routing-item {
  display: inline-flex;
  align-items: center;
  flex: none;
  gap: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-credential-routing-meta {
  gap: var(--modern-space-2);
}
.modern-credential-routing-item {
  min-height: var(--modern-space-5);
}
.modern-credential-priority-chip {
  display: inline-flex;
  flex: none;
  padding: 0;
}
.modern-credential-priority-editor {
  display: inline-flex;
  flex: none;
}
.modern-credential-priority-input {
  width: calc(var(--modern-inline-number-width) + 2 * var(--modern-control-xxs));
}
.modern-credential-priority-actions {
  display: inline-flex;
  align-items: center;
  flex: none;
}
</style>
