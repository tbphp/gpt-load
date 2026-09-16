<script setup lang="ts">
import { PencilLine, RotateCcw } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  concurrencyQueryKey,
  concurrencyQueryOptions,
  updateConcurrency,
  type ConcurrencyScope,
} from '@/app/resources/concurrency'
import AppButton from '@/components/ui/AppButton.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import IconButton from '@/components/ui/IconButton.vue'

const props = withDefaults(
  defineProps<{
    scope: ConcurrencyScope
    id?: number
    editable?: boolean
    disabled?: boolean
    compact?: boolean
    showLabel?: boolean
    detailed?: boolean
    concise?: boolean
    expanded?: boolean
    deferred?: boolean
  }>(),
  {
    id: 0,
    editable: false,
    disabled: false,
    compact: false,
    showLabel: true,
    detailed: false,
    concise: false,
    expanded: false,
    deferred: false,
  },
)
const emit = defineEmits<{
  'state-change': [state: { dirty: boolean; valid: boolean }]
}>()
const { t, n } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()
const query = useQuery(computed(() => concurrencyQueryOptions(client, props.scope, props.id)))
const editing = ref(false)
const editorInitialized = ref(false)
const pending = ref(false)
const failed = ref(false)
const saved = ref(false)
const restoringDefault = ref(false)
const mode = ref('inherit')
const value = ref('1')
const isDefault = computed(() => props.scope.startsWith('default_'))
const editorVisible = computed(() => props.expanded || editing.value)
const valid = computed(
  () =>
    mode.value !== 'custom' ||
    (/^\d+$/u.test(value.value) && Number(value.value) >= 1 && Number(value.value) <= 1_000_000),
)
const selectedMaximum = computed<number | null | undefined>(() => {
  if (mode.value === 'inherit') return null
  if (mode.value === 'unlimited') return 0
  if (!valid.value) return undefined
  return Number(value.value)
})
const dirty = computed(
  () =>
    query.data.value !== undefined &&
    selectedMaximum.value !== undefined &&
    selectedMaximum.value !== query.data.value.max_concurrency,
)
const pendingRestore = computed(
  () =>
    props.deferred &&
    !props.expanded &&
    restoringDefault.value &&
    dirty.value &&
    mode.value === 'inherit',
)
const limit = computed(() =>
  query.data.value?.effective_max_concurrency === 0
    ? t('concurrency.unlimited')
    : n(query.data.value?.effective_max_concurrency ?? 0),
)
const detailedLimit = computed(() =>
  query.data.value?.effective_max_concurrency === 0
    ? t('concurrency.none')
    : n(query.data.value?.effective_max_concurrency ?? 0),
)
const conciseValue = computed(() => {
  const data = query.data.value
  if (!data) return ''
  const maximum = data.effective_max_concurrency === 0 ? '--' : n(data.effective_max_concurrency)
  return `${n(data.current_concurrency)}/${maximum}`
})
const options = computed(() => [
  {
    value: 'inherit',
    label: t(
      isDefault.value || props.scope === 'global' ? 'concurrency.default' : 'concurrency.inherit',
    ),
  },
  { value: 'unlimited', label: t('concurrency.unlimited') },
  { value: 'custom', label: t('concurrency.custom') },
])

watch(
  () => [props.scope, props.id],
  () => {
    editing.value = false
    editorInitialized.value = false
    failed.value = false
    saved.value = false
    restoringDefault.value = false
  },
)
function initializeEditor(): void {
  if (!query.data.value) return
  const maximum = query.data.value?.max_concurrency
  mode.value =
    maximum === null || maximum === undefined ? 'inherit' : maximum === 0 ? 'unlimited' : 'custom'
  value.value = String(maximum || query.data.value?.effective_max_concurrency || 1)
  editorInitialized.value = true
}
watch(
  () => query.data.value,
  (data) => {
    if (props.expanded && data && !editorInitialized.value) initializeEditor()
  },
  { immediate: true },
)
watch(
  () => ({ dirty: dirty.value, valid: valid.value }),
  (state) => emit('state-change', state),
  { immediate: true },
)
function edit(): void {
  if (!editorInitialized.value || !dirty.value) initializeEditor()
  failed.value = false
  saved.value = false
  restoringDefault.value = false
  editing.value = true
}
async function persist(ignoreDisabled: boolean): Promise<boolean> {
  if (!valid.value || pending.value || (!ignoreDisabled && props.disabled)) return false
  if (!dirty.value) {
    editing.value = false
    return true
  }
  pending.value = true
  failed.value = false
  try {
    await updateConcurrency(
      client,
      props.scope,
      props.id,
      mode.value === 'inherit' ? null : mode.value === 'unlimited' ? 0 : Number(value.value),
    )
    await queryClient.invalidateQueries({ queryKey: concurrencyQueryKey })
    editing.value = false
    saved.value = true
    restoringDefault.value = false
    return true
  } catch {
    failed.value = true
    return false
  } finally {
    pending.value = false
  }
}
async function save(): Promise<void> {
  await persist(false)
}
function handleEditorEnter(): void {
  if (!props.deferred) void save()
}
function restoreDefault(): void {
  mode.value = 'inherit'
  failed.value = false
  saved.value = false
  restoringDefault.value = true
  editing.value = false
}
async function commit(): Promise<boolean> {
  return persist(true)
}
function pendingValue(): number | null | undefined {
  return dirty.value ? selectedMaximum.value : undefined
}
async function synchronize(): Promise<void> {
  await queryClient.invalidateQueries({
    queryKey: [...concurrencyQueryKey, props.scope, props.id],
    exact: true,
  })
  reset()
}
function reset(): void {
  editorInitialized.value = false
  initializeEditor()
  editing.value = false
  failed.value = false
  saved.value = false
  restoringDefault.value = false
}

defineExpose({ commit, pendingValue, synchronize, reset })
</script>

<template>
  <div
    class="concurrency-control"
    :class="{
      'concurrency-control--compact': compact,
      'concurrency-control--deferred-editing': deferred && !expanded && editing,
    }"
  >
    <div class="concurrency-control__summary">
      <span v-if="showLabel">{{ t(`concurrency.scopes.${scope}`) }}</span>
      <strong
        v-if="concise && query.data.value"
        :class="{
          'concurrency-control__full':
            query.data.value.effective_max_concurrency > 0 &&
            query.data.value.current_concurrency >= query.data.value.effective_max_concurrency,
        }"
      >
        {{ conciseValue }}
      </strong>
      <dl v-else-if="detailed && query.data.value" class="concurrency-control__details">
        <div>
          <dt>{{ t('concurrency.currentRequests') }}</dt>
          <dd>{{ n(query.data.value.current_concurrency) }}</dd>
        </div>
        <div>
          <dt>{{ t('concurrency.limit') }}</dt>
          <dd>{{ detailedLimit }}</dd>
        </div>
      </dl>
      <strong
        v-else-if="query.data.value"
        :class="{
          'concurrency-control__full':
            query.data.value.effective_max_concurrency > 0 &&
            query.data.value.current_concurrency >= query.data.value.effective_max_concurrency,
        }"
      >
        <template v-if="pendingRestore">{{ t('concurrency.unlimited') }}</template>
        <template v-else>
          <template
            v-if="
              !isDefault &&
              !(scope === 'global' && query.data.value.effective_max_concurrency === 0)
            "
            >{{ n(query.data.value.current_concurrency)
            }}<template v-if="scope !== 'upstream'"> / </template></template
          >
          <template v-if="scope !== 'upstream'">{{ limit }}</template>
        </template>
      </strong>
      <span v-else>—</span>
      <AppTooltip
        v-if="editable && scope !== 'upstream' && !editorVisible"
        :content="t('concurrency.edit')"
      >
        <IconButton
          class="concurrency-control__edit"
          variant="ghost"
          tone="action"
          size="xs"
          :label="t('concurrency.edit')"
          :disabled="disabled || !query.data.value"
          @click="edit"
        >
          <PencilLine :size="12" aria-hidden="true" />
        </IconButton>
      </AppTooltip>
    </div>
    <span v-if="query.isError.value" class="concurrency-control__error" role="status">{{
      t('concurrency.loadFailed')
    }}</span>
    <span v-else-if="saved" role="status">{{ t('concurrency.saved') }}</span>
    <div
      v-if="editorVisible"
      class="concurrency-control__editor"
      @keydown.enter.prevent="handleEditorEnter"
    >
      <p>{{ t(deferred ? 'concurrency.deferredHelp' : 'concurrency.help') }}</p>
      <p v-if="query.data.value?.shared">{{ t('concurrency.shared') }}</p>
      <div
        class="concurrency-control__fields"
        :class="{ 'concurrency-control__fields--with-reset': deferred && !expanded }"
      >
        <AppSelect
          v-model="mode"
          :label="t('concurrency.mode')"
          :options="options"
          :disabled="pending || disabled"
          size="sm"
        />
        <AppTooltip
          v-if="deferred && !expanded && mode !== 'inherit'"
          :content="t('concurrency.restoreDefault')"
        >
          <IconButton
            class="concurrency-control__reset"
            variant="ghost"
            tone="warning"
            size="xs"
            :label="t('concurrency.restoreDefault')"
            :disabled="pending || disabled"
            @click="restoreDefault"
          >
            <RotateCcw :size="14" aria-hidden="true" />
          </IconButton>
        </AppTooltip>
        <AppTextInput
          v-if="mode === 'custom'"
          v-model="value"
          class="concurrency-control__maximum"
          inputmode="numeric"
          :label="t('concurrency.maximum')"
          :invalid="!valid"
          :disabled="pending || disabled"
          size="sm"
        />
      </div>
      <p v-if="!valid" class="concurrency-control__error">{{ t('concurrency.invalid') }}</p>
      <p v-if="failed" class="concurrency-control__error" role="alert">
        {{ t('concurrency.saveFailed') }}
      </p>
      <div v-if="!deferred" class="concurrency-control__actions">
        <AppButton
          v-if="!expanded"
          type="button"
          variant="secondary"
          size="compact"
          :disabled="pending"
          @click="editing = false"
          >{{ t('concurrency.cancel') }}</AppButton
        >
        <AppButton
          type="button"
          size="compact"
          :disabled="pending || disabled || !valid"
          @click="save"
          >{{ t('concurrency.save') }}</AppButton
        >
      </div>
    </div>
    <small v-else-if="!compact && query.data.value && scope !== 'upstream'">
      <template v-if="pendingRestore">{{ t('concurrency.resetPending') }}</template>
      <template v-else>
        {{
          t(
            query.data.value.source === 'default'
              ? 'concurrency.inherited'
              : 'concurrency.overridden',
          )
        }}<template v-if="query.data.value.shared"> · {{ t('concurrency.shared') }}</template>
      </template>
    </small>
  </div>
</template>

<style scoped>
.concurrency-control {
  display: grid;
  gap: 6px;
  padding: 12px 0;
  font-size: 13px;
}
.concurrency-control--compact {
  padding: 4px 0;
  font-size: 12px;
}
.concurrency-control--deferred-editing {
  border-left: 2px solid var(--color-action);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding-right: 10px;
  padding-left: 12px;
}
.concurrency-control__summary,
.concurrency-control__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.concurrency-control__summary > span,
small,
p {
  color: var(--color-text-muted);
}
.concurrency-control__edit {
  width: 22px;
  min-height: 22px;
  height: 22px;
  flex: none;
  margin-left: 2px;
}
strong {
  font-variant-numeric: tabular-nums;
}
.concurrency-control__details {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  align-items: baseline;
  gap: 3px 6px;
  margin: 0;
  font-size: var(--text-label-xs);
}
.concurrency-control__details > div {
  display: contents;
}
.concurrency-control__details dt {
  color: var(--color-text-faint);
}
.concurrency-control__details dd {
  min-width: 0;
  margin: 0;
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
}
.concurrency-control__editor {
  display: grid;
  gap: 10px;
  padding: 10px 0;
  max-width: 480px;
}
.concurrency-control__editor p {
  max-width: 440px;
  margin: 0;
}
.concurrency-control__fields {
  display: grid;
  grid-template-columns: minmax(0, 440px);
  align-items: center;
  gap: 10px 8px;
}
.concurrency-control__fields--with-reset {
  grid-template-columns: minmax(0, 440px) 28px;
}
.concurrency-control__maximum {
  grid-column: 1;
}
.concurrency-control__reset {
  width: 27px;
  min-height: 27px;
  height: 27px;
}
.concurrency-control__error,
.concurrency-control__full {
  color: var(--color-danger, #c2410c);
}
</style>
