<script setup lang="ts">
import { protocolLabel, sortProtocols } from '@modern/i18n/protocols'
import {
  ArrowDown,
  ArrowUp,
  Braces,
  ChevronDown,
  ChevronRight,
  Copy,
  Info,
  Plus,
  Trash2,
} from '@lucide/vue'
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupModel, ParameterRule } from '@modern/api/group-detail'
import {
  AppProtocolTag,
  AppBadge,
  AppButton,
  AppIconButton,
  AppOverflowText,
  AppModelSelect,
  AppSegmentedField,
  AppSelect,
  AppTextArea,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import {
  blankParameter,
  flattenParameterSet,
  inferParameterType,
  inspectParameterRule,
  parameterRuleDraft,
  parameterSetDrafts,
  parameterValue,
  parameterValueTypes,
  type ParameterActionDraft,
  type ParameterRuleDraft,
  type ParameterValueType,
} from './parameter-rule-draft'
import { matchesModel } from '@modern/components/ui/model-match'

const props = defineProps<{
  modelValue: ParameterRule[]
  protocols: readonly string[]
  models: readonly GroupModel[]
  disabled?: boolean
  attempted?: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: ParameterRule[]]
  'update:valid': [value: boolean]
}>()
const { t, n } = useI18n()
const instanceId = useId()
let nextKey = 0
const key = () => nextKey++
const rows = ref(props.modelValue.map((rule) => parameterRuleDraft(rule, key)))
const elements = new Map<number, HTMLElement>()
const announcement = ref('')
const results = computed(
  () => new Map(rows.value.map((row) => [row.key, inspectParameterRule(row)])),
)
const valid = computed(() => rows.value.every((row) => Boolean(results.value.get(row.key)?.value)))
const signatureOf = (rules: ParameterRuleDraft[]) =>
  JSON.stringify(
    rules.map((row) => [
      row.protocol,
      row.model,
      row.json,
      row.setText,
      row.actions.map((action) => [
        action.operation,
        action.path,
        action.type,
        action.typePinned,
        action.text,
      ]),
    ]),
  )
const signature = computed(() => signatureOf(rows.value))
let synchronized = signature.value
let emittedValue = JSON.stringify(props.modelValue)

watch(valid, (value) => emit('update:valid', value), { immediate: true })
watch(signature, (value) => {
  if (value === synchronized || !valid.value) return
  const rules = rows.value.map((row) => results.value.get(row.key)!.value!)
  synchronized = value
  emittedValue = JSON.stringify(rules)
  emit('update:modelValue', rules)
})
watch(
  () => props.modelValue,
  (value) => {
    const encoded = JSON.stringify(value)
    if (encoded === emittedValue) return
    const next = value.map((rule) => parameterRuleDraft(rule, key))
    emittedValue = encoded
    synchronized = signatureOf(next)
    rows.value = next
  },
  { deep: true },
)

const protocolOptions = computed(() => [
  { value: '', label: t('parameterRules.allProtocols') },
  ...sortProtocols([
    ...props.protocols,
    ...rows.value.map((row) => row.protocol).filter(Boolean),
  ]).map((value) => ({ value, label: protocolLabel(value, t) })),
])
const modelNames = computed(() =>
  [...new Set(props.models.map((model) => model.clientModel))].sort(),
)
const operationOptions = computed(() =>
  ['set', 'remove'].map((value) => ({
    value,
    label: t('parameterRules.operations.' + value),
  })),
)
const typeOptions = computed(() =>
  parameterValueTypes.map((value) => ({
    value,
    label: t('parameterRules.types.' + value),
  })),
)
const booleanOptions = [
  { value: 'true', label: 'true' },
  { value: 'false', label: 'false' },
]
const summaries = computed(
  () =>
    new Map(
      rows.value.map((row): [number, string[]] => {
        const entries: string[] = []
        if (row.json) {
          for (const entry of flattenParameterSet(results.value.get(row.key)?.setValue ?? {}))
            entries.push(`${entry.path} = ${JSON.stringify(entry.value)}`)
        }
        for (const action of visibleActions(row).filter((item) => !blankParameter(item))) {
          if (action.operation === 'remove') entries.push(`− ${action.path}`)
          else {
            let value = action.text
            try {
              value = JSON.stringify(parameterValue(action)) ?? ''
            } catch {
              /* 无效输入也保留摘要。 */
            }
            entries.push(`${action.path || '…'} = ${value}`)
          }
        }
        return [row.key, entries]
      }),
    ),
)

function rowRef(id: number, element: unknown): void {
  if (element instanceof HTMLElement) elements.set(id, element)
  else elements.delete(id)
}
async function focusRule(row: ParameterRuleDraft): Promise<void> {
  row.open = true
  await nextTick()
  const element = elements.get(row.key)
  element?.scrollIntoView({ block: 'nearest' })
  element
    ?.querySelector<HTMLElement>(
      '[aria-invalid="true"], [data-parameter-path], [data-parameter-json], [data-add-parameter]',
    )
    ?.focus({ preventScroll: true })
}
function newAction(operation: 'set' | 'remove' = 'set'): ParameterActionDraft {
  return { key: key(), operation, path: '', type: 'text', typePinned: false, text: '' }
}
function addRule(): void {
  if (props.disabled || rows.value.length >= 100) return
  const row: ParameterRuleDraft = {
    key: key(),
    open: true,
    protocol: '',
    model: '',
    json: false,
    setText: '',
    actions: [newAction()],
  }
  rows.value.push(row)
  void focusRule(row)
}
function copyRule(index: number): void {
  const source = rows.value[index]
  if (!source || props.disabled || rows.value.length >= 100) return
  const row = {
    ...source,
    key: key(),
    open: true,
    actions: source.actions.map((action) => ({ ...action, key: key() })),
  }
  rows.value.splice(index + 1, 0, row)
  void focusRule(row)
}
function moveRule(index: number, direction: -1 | 1): void {
  const target = index + direction
  if (props.disabled || target < 0 || target >= rows.value.length) return
  const [row] = rows.value.splice(index, 1)
  if (!row) return
  rows.value.splice(target, 0, row)
  announcement.value = t('parameterRules.moved', { position: n(target + 1) })
}
function setOperation(action: ParameterActionDraft, value: string): void {
  if (props.disabled || (value !== 'set' && value !== 'remove')) return
  action.operation = value
  if (value === 'remove') {
    action.text = ''
    action.type = 'text'
    action.typePinned = false
  }
}
function setType(action: ParameterActionDraft, value: string): void {
  if (props.disabled || !parameterValueTypes.includes(value as ParameterValueType)) return
  action.type = value as ParameterValueType
  action.typePinned = true
  if (value === 'boolean') action.text = action.text.trim() === 'false' ? 'false' : 'true'
}
function finishValue(action: ParameterActionDraft): void {
  if (props.disabled || action.typePinned) return
  // 结束输入后再识别类型，避免输入中途替换控件、打断焦点。
  action.type = inferParameterType(action.text)
  if (action.type === 'boolean') action.text = action.text.trim()
}
function visibleActions(row: ParameterRuleDraft): ParameterActionDraft[] {
  return row.json ? row.actions.filter((action) => action.operation === 'remove') : row.actions
}
function viewOptions(row: ParameterRuleDraft) {
  return [
    {
      value: 'rows',
      label: t('parameterRules.rows'),
      disabled: row.json && !results.value.get(row.key)?.canSwitch,
    },
    { value: 'json', label: 'JSON', disabled: !row.json && !results.value.get(row.key)?.canSwitch },
  ]
}
function setView(row: ParameterRuleDraft, value: string): void {
  const json = value === 'json'
  const result = results.value.get(row.key)
  if (props.disabled || row.json === json || !result?.canSwitch || !result.setValue) return
  if (json) row.setText = JSON.stringify(result.setValue, null, 2)
  else
    row.actions = [
      ...parameterSetDrafts(result.setValue, key),
      ...row.actions.filter((action) => action.operation === 'remove'),
    ]
  row.json = json
}
function formatJSON(row: ParameterRuleDraft): void {
  const result = results.value.get(row.key)
  if (props.disabled || !result?.canFormat || !result.setValue) return
  row.setText = JSON.stringify(result.setValue, null, 2)
}
function fieldError(
  row: ParameterRuleDraft,
  action: ParameterActionDraft,
  field: 'path' | 'value',
): string | undefined {
  const code = results.value.get(row.key)?.fields.get(action.key)?.[field]
  return code ? t('parameterRules.errors.' + code) : undefined
}
function modelDescription(row: ParameterRuleDraft): string {
  const pattern = row.model.trim()
  const count = results.value.get(row.key)?.modelError
    ? 0
    : modelNames.value.filter((name) => matchesModel(name, pattern)).length
  return t('parameterRules.matches', { count: n(count) })
}
defineExpose({
  focusFirstInvalid: async () => {
    const row = rows.value.find((item) => !results.value.get(item.key)?.value)
    if (row) await focusRule(row)
  },
})
</script>

<template>
  <div class="modern-parameter-editor">
    <div class="modern-parameter-toolbar">
      <p>{{ t('parameterRules.order') }}</p>
      <AppButton
        :icon="Plus"
        variant="outline"
        size="xs"
        :disabled="disabled || rows.length >= 100"
        @click="addRule"
      >
        {{ t('parameterRules.addRule') }}
      </AppButton>
    </div>
    <p class="modern-sr-only" role="status">{{ announcement }}</p>
    <div v-if="!rows.length" class="modern-parameter-empty">{{ t('parameterRules.empty') }}</div>
    <article
      v-for="(row, index) in rows"
      :key="row.key"
      :ref="(element) => rowRef(row.key, element)"
      class="modern-parameter-rule"
      :class="{ 'is-invalid': !results.get(row.key)?.value && (attempted || !row.open) }"
    >
      <header class="modern-parameter-heading">
        <AppButton
          class="modern-parameter-toggle"
          variant="ghost"
          size="xs"
          :icon="row.open ? ChevronDown : ChevronRight"
          :aria-expanded="row.open"
          :aria-controls="`${instanceId}-${row.key}`"
          @click="row.open = !row.open"
        >
          <span>{{ t('parameterRules.rule', { number: n(index + 1) }) }}</span>
          <AppOverflowText
            :text="`${row.protocol ? protocolLabel(row.protocol, t) : t('parameterRules.allProtocols')} · ${row.model.trim() || t('parameterRules.allModels')}`"
          />
        </AppButton>
        <div class="modern-parameter-tools">
          <div class="modern-parameter-move">
            <AppIconButton
              :icon="ArrowUp"
              :label="t('parameterRules.moveUp')"
              size="xs"
              :disabled="disabled || index === 0"
              @click="moveRule(index, -1)"
            />
            <AppIconButton
              :icon="ArrowDown"
              :label="t('parameterRules.moveDown')"
              size="xs"
              :disabled="disabled || index === rows.length - 1"
              @click="moveRule(index, 1)"
            />
          </div>
          <AppIconButton
            :icon="Copy"
            :label="t('parameterRules.duplicate')"
            size="xs"
            :disabled="disabled || rows.length >= 100"
            @click="copyRule(index)"
          />
          <AppIconButton
            :icon="Trash2"
            :label="t('parameterRules.deleteRule')"
            size="xs"
            :disabled="disabled"
            @click="rows.splice(index, 1)"
          />
        </div>
      </header>
      <div v-if="!row.open" class="modern-parameter-summary">
        <AppBadge v-if="!results.get(row.key)?.value" tone="warning" variant="plain">{{
          t('parameterRules.incomplete')
        }}</AppBadge>
        <span
          v-for="(entry, entryIndex) in summaries.get(row.key)?.slice(0, 3)"
          :key="entryIndex"
          class="modern-parameter-chip"
        >
          <AppOverflowText :text="entry" />
        </span>
        <span v-if="(summaries.get(row.key)?.length ?? 0) > 3"
          >+{{ n((summaries.get(row.key)?.length ?? 0) - 3) }}</span
        >
        <span v-else-if="!summaries.get(row.key)?.length">{{
          t('parameterRules.addParameter')
        }}</span>
      </div>
      <div v-else :id="`${instanceId}-${row.key}`" class="modern-parameter-body">
        <div class="modern-parameter-match">
          <AppSelect
            v-model="row.protocol"
            :label="t('parameterRules.protocol')"
            :options="protocolOptions"
            size="xs"
            :disabled="disabled"
          >
            <template #value="{ value, label }"
              ><AppProtocolTag v-if="value" :protocol="value" /><span v-else>{{
                label
              }}</span></template
            >
            <template #option="{ option }"
              ><AppProtocolTag v-if="option.value" :protocol="option.value" /><span v-else>{{
                option.label
              }}</span></template
            >
          </AppSelect>
          <AppModelSelect
            v-model="row.model"
            :label="t('parameterRules.model')"
            :models="modelNames"
            size="xs"
            :disabled="disabled"
            :description="modelDescription(row)"
            :error="
              results.get(row.key)?.modelError ? t('parameterRules.errors.modelPattern') : undefined
            "
          />
        </div>
        <div class="modern-parameter-edit-tools">
          <div class="modern-parameter-view">
            <AppSegmentedField
              :model-value="row.json ? 'json' : 'rows'"
              :label="t('parameterRules.editMode')"
              label-hidden
              :options="viewOptions(row)"
              size="xs"
              :disabled="disabled"
              @update:model-value="setView(row, $event)"
            />
          </div>
          <AppButton
            v-if="row.json"
            :icon="Braces"
            variant="ghost"
            size="xs"
            :disabled="disabled || !results.get(row.key)?.canFormat"
            @click="formatJSON(row)"
            >{{ t('parameterRules.formatJSON') }}</AppButton
          >
        </div>
        <AppTextArea
          v-if="row.json"
          v-model="row.setText"
          :label="t('parameterRules.setJSON')"
          label-hidden
          :rows="4"
          size="xs"
          mono
          :disabled="disabled"
          spellcheck="false"
          data-parameter-json
          :error="
            results.get(row.key)?.setError
              ? t('parameterRules.errors.' + results.get(row.key)?.setError)
              : undefined
          "
        />
        <div
          v-if="visibleActions(row).length"
          class="modern-parameter-labels"
          :class="{ 'is-json-remove': row.json }"
          aria-hidden="true"
        >
          <span v-if="!row.json">{{ t('parameterRules.operation') }}</span>
          <span>{{ t(row.json ? 'parameterRules.removePaths' : 'parameterRules.path') }}</span>
          <template v-if="!row.json"
            ><span>{{ t('parameterRules.type') }}</span
            ><span>{{ t('parameterRules.value') }}</span></template
          >
        </div>
        <div
          v-for="action in visibleActions(row)"
          :key="action.key"
          class="modern-parameter-action"
          :class="{ 'is-json-remove': row.json }"
        >
          <div class="modern-parameter-operation">
            <span v-if="row.json" class="modern-parameter-remove-label">{{
              t('parameterRules.operations.remove')
            }}</span>
            <AppSegmentedField
              v-else
              :model-value="action.operation"
              :label="t('parameterRules.operation')"
              label-hidden
              :options="operationOptions"
              size="xs"
              :disabled="disabled"
              @update:model-value="setOperation(action, $event)"
            />
          </div>
          <div class="modern-parameter-path">
            <AppTextField
              v-model="action.path"
              :label="t('parameterRules.path')"
              label-hidden
              placeholder="reasoning/effort"
              size="xs"
              :disabled="disabled"
              :error="fieldError(row, action, 'path')"
              data-parameter-path
              autocomplete="off"
              spellcheck="false"
            />
          </div>
          <div v-if="action.operation === 'set'" class="modern-parameter-type">
            <AppSelect
              :model-value="action.type"
              :label="t('parameterRules.type')"
              label-hidden
              :options="typeOptions"
              size="xs"
              :disabled="disabled"
              @update:model-value="setType(action, $event)"
            />
          </div>
          <div v-if="action.operation === 'set'" class="modern-parameter-value">
            <AppSegmentedField
              v-if="action.type === 'boolean'"
              v-model="action.text"
              :label="t('parameterRules.value')"
              label-hidden
              :options="booleanOptions"
              size="xs"
              :disabled="disabled"
            />
            <div v-else-if="action.type === 'null'" class="modern-parameter-null">
              <AppBadge variant="plain" mono>null</AppBadge>
            </div>
            <AppTextArea
              v-else-if="action.type === 'json'"
              v-model="action.text"
              :label="t('parameterRules.value')"
              label-hidden
              :rows="2"
              size="xs"
              mono
              :disabled="disabled"
              :error="fieldError(row, action, 'value')"
              spellcheck="false"
              @change="finishValue(action)"
            />
            <AppTextField
              v-else
              v-model="action.text"
              :label="t('parameterRules.value')"
              label-hidden
              :placeholder="t('parameterRules.valuePlaceholder')"
              :inputmode="action.type === 'number' ? 'decimal' : 'text'"
              size="xs"
              :disabled="disabled"
              :error="fieldError(row, action, 'value')"
              autocomplete="off"
              spellcheck="false"
              @change="finishValue(action)"
            />
          </div>
          <AppIconButton
            class="modern-parameter-delete"
            :icon="Trash2"
            :label="t('parameterRules.deleteParameter')"
            size="xs"
            :disabled="disabled"
            @click="row.actions = row.actions.filter((item) => item.key !== action.key)"
          />
        </div>
        <p
          v-if="attempted && results.get(row.key)?.actionRequired"
          class="modern-parameter-error"
          role="alert"
        >
          {{ t('parameterRules.errors.actionRequired') }}
        </p>
        <p v-if="!results.get(row.key)?.canSwitch" class="modern-parameter-note">
          {{ t('parameterRules.switchBlocked') }}
        </p>
        <p v-if="results.get(row.key)?.crossing" class="modern-parameter-note">
          {{ t('parameterRules.crossing') }}
        </p>
        <AppButton
          :icon="Plus"
          variant="ghost"
          size="xs"
          :disabled="disabled"
          data-add-parameter
          @click="row.actions.push(newAction(row.json ? 'remove' : 'set'))"
          >{{ t(row.json ? 'parameterRules.addRemove' : 'parameterRules.addParameter') }}</AppButton
        >
      </div>
    </article>
    <AppTooltip
      v-if="rows.length"
      :label="
        [
          t('parameterRules.modelHelp'),
          t('parameterRules.typeHelp'),
          t('parameterRules.pathHelp'),
          t('parameterRules.mergeHelp'),
          t('parameterRules.jsonHelp'),
        ].join('\n')
      "
    >
      <AppButton :icon="Info" variant="text" size="xs">{{ t('parameterRules.help') }}</AppButton>
    </AppTooltip>
  </div>
</template>

<style scoped>
.modern-parameter-editor {
  --modern-parameter-action-size: var(--modern-control-xs);
  --modern-parameter-columns: 6rem minmax(0, 1.3fr) 6rem minmax(0, 1fr)
    var(--modern-parameter-action-size);
  container: modern-parameter-editor / inline-size;
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-parameter-toolbar,
.modern-parameter-heading,
.modern-parameter-tools,
.modern-parameter-move,
.modern-parameter-edit-tools {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-parameter-toolbar {
  flex-wrap: wrap;
  justify-content: space-between;
}
.modern-parameter-toolbar p,
.modern-parameter-note,
.modern-parameter-empty,
.modern-parameter-help {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-parameter-empty {
  padding: var(--modern-space-4);
  background: var(--modern-subtle);
  border-radius: var(--modern-radius-control);
}
.modern-parameter-rule {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
}
.modern-parameter-rule.is-invalid {
  border-color: color-mix(in srgb, var(--modern-danger) 35%, var(--modern-border));
}
.modern-parameter-heading {
  padding: var(--modern-space-1);
  background: var(--modern-subtle);
  border-radius: var(--modern-radius-control);
}
.modern-parameter-toggle {
  flex: 1;
  min-width: 0;
  justify-content: flex-start;
  text-align: left;
}
.modern-parameter-toggle > span:first-of-type {
  flex: none;
  color: var(--modern-text);
}
.modern-parameter-toggle > span:last-child {
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
}
.modern-parameter-tools,
.modern-parameter-move {
  flex: none;
  gap: 0;
}
.modern-parameter-move {
  margin-right: var(--modern-space-1);
  padding-right: var(--modern-space-1);
  border-right: var(--modern-line-width) solid var(--modern-border);
}
.modern-parameter-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1-5);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-parameter-chip {
  min-width: 0;
  max-width: min(100%, 32ch);
  padding: var(--modern-space-0-5) var(--modern-space-1-5);
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  font-family: var(--modern-font-mono);
}
.modern-parameter-body {
  display: grid;
  gap: var(--modern-space-2);
  padding: var(--modern-space-2);
  min-width: 0;
}
.modern-parameter-match {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.5fr);
  align-items: start;
  gap: var(--modern-space-2);
}
.modern-parameter-edit-tools {
  flex-wrap: wrap;
}
.modern-parameter-view {
  width: 8rem;
  max-width: 100%;
}
.modern-parameter-labels,
.modern-parameter-action {
  display: grid;
  grid-template-columns: var(--modern-parameter-columns);
  align-items: start;
  gap: var(--modern-space-2);
}
.modern-parameter-action {
  grid-template-areas: 'operation path type value delete';
}
.modern-parameter-labels {
  margin-bottom: calc(-1 * var(--modern-space-1));
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-parameter-action > div {
  min-width: 0;
}
.modern-parameter-operation {
  grid-area: operation;
}
.modern-parameter-path {
  grid-area: path;
}
.modern-parameter-type {
  grid-area: type;
}
.modern-parameter-value {
  grid-area: value;
}
.modern-parameter-delete {
  grid-area: delete;
}
.modern-parameter-null,
.modern-parameter-remove-label {
  display: flex;
  align-items: center;
  min-height: var(--modern-control-xs);
}
.modern-parameter-remove-label {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-parameter-error {
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
}

@container modern-parameter-editor (max-width: 520px) {
  .modern-parameter-labels {
    display: none;
  }
  .modern-parameter-action {
    grid-template-columns: 6rem 6rem minmax(0, 1fr) var(--modern-parameter-action-size);
    grid-template-areas: 'operation path path delete' '. type value .';
  }
  .modern-parameter-action + .modern-parameter-action {
    padding-top: var(--modern-space-3);
    border-top: var(--modern-line-width) solid var(--modern-border);
  }
}
@container modern-parameter-editor (max-width: 440px) {
  .modern-parameter-match {
    grid-template-columns: 1fr;
  }
  .modern-parameter-heading {
    flex-wrap: wrap;
  }
  .modern-parameter-toggle {
    flex-basis: 100%;
  }
  .modern-parameter-action {
    grid-template-columns: 6.5rem minmax(0, 1fr) var(--modern-parameter-action-size);
    grid-template-areas: 'operation operation delete' 'path path .' 'type value .';
  }
}
.modern-parameter-action.is-json-remove,
.modern-parameter-labels.is-json-remove {
  grid-template-columns: minmax(0, 1fr) var(--modern-parameter-action-size);
  grid-template-areas: 'path delete';
}
.modern-parameter-action.is-json-remove .modern-parameter-operation {
  display: none;
}
@media (max-width: 760px) {
  .modern-parameter-editor {
    --modern-parameter-action-size: var(--modern-touch-target);
  }
  .modern-parameter-null,
  .modern-parameter-remove-label {
    min-height: var(--modern-touch-target);
  }
}
</style>
