<script setup lang="ts">
import { ArrowDown, ArrowUp, ChevronDown, Copy, Plus, Trash2, X } from '@lucide/vue'
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  AccessProtocol,
  GroupModelItemDto,
  ParameterJSONValue,
  ParameterOverrideRuleDto,
} from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import { assertJSONNumbersRoundTrip, JSONNumberPrecisionError } from '@/lib/json-number'
import {
  decodeParameterPath,
  expandParameterSet,
  flattenParameterSet,
  fromParameterPointer,
  parameterPathsCross,
  toParameterPointer,
  type ParameterPathEntry,
} from '@/lib/parameter-paths'
import {
  formatValueText,
  hasEmptyParameterKey,
  inferValueKind,
  parameterValueKinds,
  ParameterValueError,
  tryValueFromText,
  valueFromText,
  valueKind,
  type ParameterValueKind,
} from '@/lib/parameter-values'

type ParamOp = 'set' | 'remove'

/** 一行参数动作。行序只是录入次序，不参与语义。 */
interface ParamRow {
  key: number
  op: ParamOp
  path: string
  valueText: string
  kind: ParameterValueKind
  /** 用户动过类型列之后就不再跟随输入推断，否则一打字又被改回去。 */
  kindPinned: boolean
}

interface RuleRow {
  key: number
  open: boolean
  protocol: string
  model: string
  /** true 时 set 用整段 JSON 编辑，remove 仍是行。 */
  json: boolean
  params: ParamRow[]
  setText: string
}

interface RuleErrors {
  model?: string
  set?: string
  action?: string
  params: Map<number, string>
}

interface SummaryChip {
  key: string
  op: ParamOp
  path: string
  value: string
}

interface RuleSummary {
  chips: SummaryChip[]
  overflow: number
}

/** 折叠行放不下太多摘要；超出的用 +N 兜住，避免静默裁切成看不见。 */
const summaryLimit = 3

const forbiddenRootFields = ['model', 'stream', 'store']

const props = withDefaults(
  defineProps<{
    modelValue: ParameterOverrideRuleDto[]
    protocols: AccessProtocol[]
    models: GroupModelItemDto[]
    disabled?: boolean
  }>(),
  { disabled: false },
)
const emit = defineEmits<{
  'update:modelValue': [value: ParameterOverrideRuleDto[]]
  'update:valid': [value: boolean]
  'update:invalid-edits': [value: boolean]
}>()
const { t } = useI18n()
const instanceId = useId()
let nextKey = 1

function newKey(): number {
  return nextKey++
}

function createSetParam({ path, value }: ParameterPathEntry): ParamRow {
  return {
    key: newKey(),
    op: 'set',
    path,
    valueText: formatValueText(value),
    kind: valueKind(value),
    kindPinned: false,
  }
}

function createRows(value: ParameterOverrideRuleDto[]): RuleRow[] {
  return value.map((rule) => ({
    key: newKey(),
    open: false,
    protocol: rule.match.protocol ?? '',
    model: rule.match.model ?? '',
    json: false,
    params: [
      ...(rule.set ? flattenParameterSet(rule.set) : []).map(createSetParam),
      ...(rule.remove ?? []).map((pointer) => ({
        key: newKey(),
        op: 'remove' as const,
        path: fromParameterPointer(pointer),
        valueText: '',
        kind: 'text' as const,
        kindPinned: false,
      })),
    ],
    setText: rule.set ? JSON.stringify(rule.set, null, 2) : '',
  }))
}

const rows = ref<RuleRow[]>(createRows(props.modelValue))
const moveAnnouncement = ref('')
const modelSuggestions = computed(() =>
  [...new Set(props.models.map(({ client_model }) => client_model))].sort((left, right) =>
    left.localeCompare(right),
  ),
)
const availableProtocols = computed(() => {
  const values = new Set<AccessProtocol>(props.protocols)
  for (const row of rows.value) {
    if (row.protocol) values.add(row.protocol as AccessProtocol)
  }
  return [...values]
})
const protocolOptions = computed(() => [
  { value: '', label: t('group.settings.parameterOverrides.allProtocols') },
  ...availableProtocols.value.map((value) => ({ value, label: value })),
])
const opOptions = computed(() => [
  { value: 'set', label: t('group.settings.parameterOverrides.opSet') },
  { value: 'remove', label: t('group.settings.parameterOverrides.opRemove') },
])
const kindOptions = computed(() =>
  parameterValueKinds.map((kind) => ({
    value: kind,
    label: t(`group.settings.parameterOverrides.kind.${kind}`),
  })),
)
const booleanOptions = computed(() => [
  { value: 'true', label: 'true' },
  { value: 'false', label: 'false' },
])

function parseSetText(text: string): Record<string, ParameterJSONValue> | undefined {
  const trimmed = text.trim()
  if (!trimmed) return {}
  const value: unknown = JSON.parse(trimmed)
  assertJSONNumbersRoundTrip(trimmed)
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return undefined
  if (hasEmptyParameterKey(value)) throw new ParameterValueError('empty-key')
  return value as Record<string, ParameterJSONValue>
}

function tryParseSetText(text: string): Record<string, ParameterJSONValue> | undefined {
  try {
    return parseSetText(text)
  } catch {
    return undefined
  }
}

/** 刚加出来还没填的行：不标红、不阻止保存，序列化时直接忽略。 */
function isBlankParam(param: ParamRow): boolean {
  if (param.path.trim()) return false
  return param.op === 'remove' || param.kind === 'null' || !param.valueText.trim()
}

function setEntries(row: RuleRow): ParameterPathEntry[] | undefined {
  if (row.json) {
    const parsed = tryParseSetText(row.setText)
    return parsed ? flattenParameterSet(parsed) : undefined
  }
  const entries: ParameterPathEntry[] = []
  for (const param of row.params) {
    if (param.op !== 'set' || isBlankParam(param)) continue
    const value = tryValueFromText(param.valueText, param.kind)
    if (value === undefined) return undefined
    entries.push({ path: param.path, value })
  }
  return entries
}

function removePointers(row: RuleRow): string[] {
  return row.params
    .filter((param) => param.op === 'remove' && !isBlankParam(param))
    .map(({ path }) => toParameterPointer(path))
}

function pathError(row: ParamRow, siblings: ParamRow[]): string {
  if (!row.path.trim()) return t('group.settings.parameterOverrides.errors.pathRequired')
  const segments = decodeParameterPath(row.path, row.op)
  if (!segments) return t('group.settings.parameterOverrides.errors.pathInvalid')
  if (forbiddenRootFields.includes((segments[0] ?? '').toLowerCase()))
    return t('group.settings.parameterOverrides.errors.forbiddenField')
  for (const other of siblings) {
    if (other.key === row.key) continue
    if (other.op === row.op && other.path === row.path)
      return t('group.settings.parameterOverrides.errors.pathDuplicate')
    // 同为 set 时祖先与后代互斥：还原成嵌套对象会互相顶掉。
    if (row.op === 'set' && other.op === 'set' && parameterPathsCross(row.path, other.path))
      return t('group.settings.parameterOverrides.errors.pathAncestor')
  }
  return ''
}

function valueError(row: ParamRow): string {
  // 空值类型没有值可填；文本类型允许空字符串。
  if (row.kind === 'null') return ''
  if (!row.valueText.trim() && row.kind !== 'text')
    return t('group.settings.parameterOverrides.errors.valueRequired')
  try {
    valueFromText(row.valueText, row.kind)
    return ''
  } catch (cause: unknown) {
    if (cause instanceof JSONNumberPrecisionError)
      return t('group.settings.parameterOverrides.errors.unsafeNumber')
    if (cause instanceof ParameterValueError && cause.message === 'number')
      return t('group.settings.parameterOverrides.errors.valueNumber')
    if (cause instanceof ParameterValueError && cause.message === 'boolean')
      return t('group.settings.parameterOverrides.errors.valueBoolean')
    if (cause instanceof ParameterValueError && cause.message === 'empty-key')
      return t('group.settings.parameterOverrides.errors.emptyKey')
    return t('group.settings.parameterOverrides.errors.valueJSON')
  }
}

function ruleErrors(row: RuleRow): RuleErrors {
  const errors: RuleErrors = { params: new Map() }
  const model = row.model.trim()
  if (model.includes('*') && !/^[^*]+\*$/u.test(model)) {
    errors.model = t('group.settings.parameterOverrides.errors.modelPattern')
  }

  const visible = row.json ? row.params.filter(({ op }) => op === 'remove') : row.params
  const filled = visible.filter((param) => !isBlankParam(param))
  for (const param of filled) {
    const error = pathError(param, filled) || (param.op === 'set' ? valueError(param) : '')
    if (error) errors.params.set(param.key, error)
  }

  let parsedSet: Record<string, ParameterJSONValue> | undefined
  if (row.json) {
    try {
      parsedSet = parseSetText(row.setText)
      if (!parsedSet) {
        errors.set = t('group.settings.parameterOverrides.errors.setObject')
      } else {
        const roots = new Set(
          flattenParameterSet(parsedSet).map(({ path }) =>
            (decodeParameterPath(path, 'set')?.[0] ?? '').toLowerCase(),
          ),
        )
        if (forbiddenRootFields.some((field) => roots.has(field)))
          errors.set = t('group.settings.parameterOverrides.errors.forbiddenField')
      }
    } catch (cause: unknown) {
      errors.set = t(
        cause instanceof JSONNumberPrecisionError
          ? 'group.settings.parameterOverrides.errors.unsafeNumber'
          : cause instanceof ParameterValueError && cause.message === 'empty-key'
            ? 'group.settings.parameterOverrides.errors.emptyKey'
            : 'group.settings.parameterOverrides.errors.invalidJSON',
      )
    }
  }

  const hasSet = row.json
    ? Object.keys(parsedSet ?? {}).length > 0
    : filled.some(({ op }) => op === 'set')
  const hasRemove = row.params.some((param) => param.op === 'remove' && !isBlankParam(param))
  if (!hasSet && !hasRemove) {
    errors.action = t('group.settings.parameterOverrides.errors.actionRequired')
  }
  return errors
}

const errorsByRule = computed(
  () => new Map(rows.value.map((row) => [row.key, ruleErrors(row)] as const)),
)
const valid = computed(() =>
  [...errorsByRule.value.values()].every(
    (errors) => !errors.model && !errors.set && !errors.action && errors.params.size === 0,
  ),
)

function ruleInvalid(row: RuleRow): boolean {
  const errors = errorsByRule.value.get(row.key)
  return Boolean(errors && (errors.model || errors.set || errors.action || errors.params.size > 0))
}

/** 设置与删除只在路径相交时才互相影响，这时后端固定先删后设。 */
function pathsCrossing(row: RuleRow): boolean {
  const removes = row.params.filter(({ op }) => op === 'remove').map(({ path }) => path)
  if (removes.length === 0) return false
  const sets = (setEntries(row) ?? []).map(({ path }) => path)
  return removes.some((left) => sets.some((right) => parameterPathsCross(left, right)))
}

const summaryByRule = computed(
  () =>
    new Map<number, RuleSummary>(
      rows.value.map((row) => {
        const summary = buildSummary(row)
        return [
          row.key,
          {
            chips: summary.slice(0, summaryLimit),
            overflow: Math.max(0, summary.length - summaryLimit),
          },
        ] as const
      }),
    ),
)

function buildSummary(row: RuleRow): SummaryChip[] {
  const chips: SummaryChip[] = []
  if (row.json) {
    for (const { path, value } of setEntries(row) ?? []) {
      chips.push({ key: `s${path}`, op: 'set', path, value: previewValue(value) })
    }
    for (const param of row.params) {
      if (param.op === 'remove')
        chips.push({ key: `r${param.key}`, op: 'remove', path: param.path, value: '' })
    }
    return chips
  }
  for (const param of row.params) {
    const value = param.op === 'set' ? tryValueFromText(param.valueText, param.kind) : undefined
    chips.push({
      key: `p${param.key}`,
      op: param.op,
      path: param.path,
      value: value === undefined ? '' : previewValue(value),
    })
  }
  return chips
}

/** 摘要里的值去掉字符串引号，其余类型保留字面量。 */
function previewValue(value: ParameterJSONValue): string {
  return typeof value === 'string' ? value : formatValueText(value)
}

function serializeRows(): ParameterOverrideRuleDto[] {
  return rows.value.map((row) => {
    const protocol = row.protocol as AccessProtocol
    const model = row.model.trim()
    const set = expandParameterSet(setEntries(row) ?? [])
    const remove = removePointers(row)
    return {
      match: {
        ...(protocol ? { protocol } : {}),
        ...(model ? { model } : {}),
      },
      ...(Object.keys(set).length > 0 ? { set } : {}),
      ...(remove.length > 0 ? { remove } : {}),
    }
  })
}

watch(
  rows,
  () => {
    emit('update:valid', valid.value)
    emit('update:invalid-edits', !valid.value)
    if (valid.value) emit('update:modelValue', serializeRows())
  },
  { deep: true, immediate: true },
)

function addRule(): void {
  rows.value.push({
    key: newKey(),
    open: true,
    protocol: '',
    model: '',
    json: false,
    params: [
      { key: newKey(), op: 'set', path: '', valueText: '', kind: 'text', kindPinned: false },
    ],
    setText: '',
  })
}

function copyRule(index: number): void {
  const source = rows.value[index]
  if (!source) return
  rows.value.splice(index + 1, 0, {
    key: newKey(),
    open: true,
    protocol: source.protocol,
    model: source.model,
    json: source.json,
    params: source.params.map((param) => ({ ...param, key: newKey() })),
    setText: source.setText,
  })
}

function moveRule(index: number, offset: -1 | 1): void {
  const target = index + offset
  if (target < 0 || target >= rows.value.length) return
  const [row] = rows.value.splice(index, 1)
  if (!row) return
  rows.value.splice(target, 0, row)
  moveAnnouncement.value = t('group.settings.parameterOverrides.moved', { position: target + 1 })
}

function removeRule(index: number): void {
  rows.value.splice(index, 1)
}

/** 新增行追加末尾，切换动作原地不动：行序不参与语义，不该跳走。 */
function addParam(row: RuleRow): void {
  row.params.push({
    key: newKey(),
    op: row.json ? 'remove' : 'set',
    path: '',
    valueText: '',
    kind: 'text',
    kindPinned: false,
  })
}

function setParamOp(param: ParamRow, value: string): void {
  if (value !== 'set' && value !== 'remove') return
  param.op = value
  if (value === 'remove') {
    param.valueText = ''
    param.kind = 'text'
    param.kindPinned = false
  }
}

function setParamKind(param: ParamRow, value: string): void {
  if (!(parameterValueKinds as readonly string[]).includes(value)) return
  param.kind = value as ParameterValueKind
  param.kindPinned = true
  if (param.kind === 'boolean' && param.valueText.trim() !== 'false') param.valueText = 'true'
}

function setParamValue(param: ParamRow, value: string): void {
  param.valueText = value
  if (!param.kindPinned) param.kind = inferValueKind(value)
}

function removeParam(row: RuleRow, key: number): void {
  row.params = row.params.filter((param) => param.key !== key)
}

const switchBlocked = computed(
  () =>
    new Map(
      rows.value.map((row) => {
        const errors = errorsByRule.value.get(row.key)
        const blocked = row.json
          ? Boolean(errors?.set) || tryParseSetText(row.setText) === undefined
          : setEntries(row) === undefined ||
            [...(errors?.params ?? [])].some(([key]) =>
              row.params.some((param) => param.key === key && param.op === 'set'),
            )
        return [row.key, blocked] as const
      }),
    ),
)

function toggleJSON(row: RuleRow): void {
  if (switchBlocked.value.get(row.key)) return
  if (row.json) {
    const parsed = tryParseSetText(row.setText)
    if (!parsed) return
    row.params = [
      ...flattenParameterSet(parsed).map(createSetParam),
      ...row.params.filter(({ op }) => op === 'remove'),
    ]
    row.json = false
    return
  }
  const entries = setEntries(row)
  if (entries === undefined) return
  const set = expandParameterSet(entries)
  row.setText = Object.keys(set).length > 0 ? JSON.stringify(set, null, 2) : ''
  row.json = true
}

function formatSetText(row: RuleRow): void {
  const parsed = tryParseSetText(row.setText)
  if (parsed) row.setText = Object.keys(parsed).length > 0 ? JSON.stringify(parsed, null, 2) : ''
}

function protocolLabel(value: string): string {
  return value || t('group.settings.parameterOverrides.allProtocols')
}

function modelLabel(row: RuleRow): string {
  return row.model.trim() || t('group.settings.parameterOverrides.allModels')
}

function matchCount(model: string): number | undefined {
  const value = model.trim()
  if (!value || (value.includes('*') && !/^[^*]+\*$/u.test(value))) return undefined
  const prefix = value.endsWith('*') ? value.slice(0, -1) : undefined
  return modelSuggestions.value.filter((candidate) =>
    prefix === undefined ? candidate === value : candidate.startsWith(prefix),
  ).length
}

const modelMatchCounts = computed(
  () => new Map(rows.value.map((row) => [row.key, matchCount(row.model)] as const)),
)
</script>

<template>
  <div class="parameter-rules">
    <div class="parameter-rules__bar">
      <span class="parameter-rules__order">
        <ArrowDown :size="13" aria-hidden="true" />
        {{ t('group.settings.parameterOverrides.order') }}
      </span>
      <AppButton size="compact" :disabled="disabled" @click="addRule">
        <Plus :size="15" aria-hidden="true" />
        {{ t('group.settings.parameterOverrides.addRule') }}
      </AppButton>
    </div>

    <p class="sr-only" aria-live="polite">{{ moveAnnouncement }}</p>

    <p v-if="rows.length === 0" class="parameter-rules__empty">
      {{ t('group.settings.parameterOverrides.empty') }}
    </p>

    <div v-else class="parameter-rules__list">
      <article
        v-for="(row, index) in rows"
        :key="row.key"
        class="parameter-rule"
        :class="{ 'parameter-rule--invalid': ruleInvalid(row) }"
      >
        <div class="parameter-rule__head">
          <button
            type="button"
            class="parameter-rule__summary"
            :aria-expanded="row.open"
            :aria-controls="`${instanceId}-rule-${row.key}`"
            @click="row.open = !row.open"
          >
            <ChevronDown class="parameter-rule__chevron" :size="15" aria-hidden="true" />
            <span class="parameter-rule__index">{{ index + 1 }}</span>
            <span class="parameter-rule__match">
              <span
                class="parameter-rule__tag"
                :class="{ 'parameter-rule__tag--any': !row.protocol }"
              >
                {{ protocolLabel(row.protocol) }}
              </span>
              <span
                class="parameter-rule__tag"
                :class="{ 'parameter-rule__tag--any': !row.model.trim() }"
              >
                {{ modelLabel(row) }}
              </span>
            </span>
            <span class="parameter-rule__summary-tail">
              <span class="parameter-rule__chips">
                <span
                  v-for="chip in summaryByRule.get(row.key)?.chips"
                  :key="chip.key"
                  class="parameter-rule__chip"
                  :class="{ 'parameter-rule__chip--drop': chip.op === 'remove' }"
                >
                  <span
                    v-if="chip.op === 'remove'"
                    class="parameter-rule__chip-mark"
                    aria-hidden="true"
                    >−</span
                  >
                  <b>{{ chip.path }}</b>
                  <span v-if="chip.op === 'set'" class="parameter-rule__chip-value">{{
                    chip.value
                  }}</span>
                </span>
              </span>
              <span v-if="summaryByRule.get(row.key)?.overflow" class="parameter-rule__chip-more"
                >+{{ summaryByRule.get(row.key)?.overflow }}</span
              >
            </span>
          </button>
          <div class="parameter-rule__tools">
            <IconButton
              variant="ghost"
              size="xs"
              :label="t('group.settings.parameterOverrides.moveUp')"
              :disabled="disabled || index === 0"
              @click="moveRule(index, -1)"
              ><ArrowUp :size="15" aria-hidden="true"
            /></IconButton>
            <IconButton
              variant="ghost"
              size="xs"
              :label="t('group.settings.parameterOverrides.moveDown')"
              :disabled="disabled || index === rows.length - 1"
              @click="moveRule(index, 1)"
              ><ArrowDown :size="15" aria-hidden="true"
            /></IconButton>
            <span class="parameter-rule__tools-gap" aria-hidden="true"></span>
            <IconButton
              variant="ghost"
              size="xs"
              :label="t('group.settings.parameterOverrides.copy')"
              :disabled="disabled"
              @click="copyRule(index)"
              ><Copy :size="15" aria-hidden="true"
            /></IconButton>
            <IconButton
              variant="ghost"
              tone="danger"
              size="xs"
              :label="t('group.settings.parameterOverrides.deleteRule')"
              :disabled="disabled"
              @click="removeRule(index)"
              ><Trash2 :size="15" aria-hidden="true"
            /></IconButton>
          </div>
        </div>

        <div v-if="row.open" :id="`${instanceId}-rule-${row.key}`" class="parameter-rule__body">
          <div class="parameter-rule__field">
            <span class="parameter-rule__label">{{
              t('group.settings.parameterOverrides.match')
            }}</span>
            <div class="parameter-rule__match-inputs">
              <AppSelect
                v-model="row.protocol"
                :label="t('group.settings.parameterOverrides.protocol')"
                :options="protocolOptions"
                :disabled="disabled"
                size="compact"
              />
              <CompactFieldError
                :id="`${instanceId}-model-${row.key}`"
                :error="errorsByRule.get(row.key)?.model"
              >
                <template #default="{ describedBy, invalid }">
                  <input
                    :id="`${instanceId}-model-${row.key}`"
                    v-model="row.model"
                    class="parameter-rule__input"
                    :list="`${instanceId}-models`"
                    :placeholder="t('group.settings.parameterOverrides.allModels')"
                    :aria-label="t('group.settings.parameterOverrides.model')"
                    :aria-describedby="describedBy"
                    :aria-invalid="invalid"
                    :disabled="disabled"
                    autocomplete="off"
                  />
                </template>
              </CompactFieldError>
              <span
                v-if="modelMatchCounts.get(row.key) !== undefined"
                class="parameter-rule__count"
                :class="{ 'parameter-rule__count--zero': modelMatchCounts.get(row.key) === 0 }"
              >
                {{
                  modelMatchCounts.get(row.key) === 0
                    ? t('group.settings.parameterOverrides.modelNoMatch')
                    : t('group.settings.parameterOverrides.modelMatch', {
                        count: modelMatchCounts.get(row.key),
                      })
                }}
              </span>
            </div>
          </div>

          <div class="parameter-rule__field parameter-rule__field--params">
            <span class="parameter-rule__label">{{
              t('group.settings.parameterOverrides.params')
            }}</span>
            <div class="parameter-rule__params-stack">
              <div v-if="row.json">
                <CompactFieldError
                  :id="`${instanceId}-set-${row.key}`"
                  :error="errorsByRule.get(row.key)?.set"
                >
                  <template #default="{ describedBy, invalid }">
                    <textarea
                      :id="`${instanceId}-set-${row.key}`"
                      v-model="row.setText"
                      class="parameter-rule__input parameter-rule__textarea"
                      :aria-label="t('group.settings.parameterOverrides.setLabel')"
                      :aria-describedby="describedBy"
                      :aria-invalid="invalid"
                      :disabled="disabled"
                      spellcheck="false"
                    ></textarea>
                  </template>
                </CompactFieldError>
              </div>

              <div
                v-if="!row.json || row.params.some(({ op }) => op === 'remove')"
                class="parameter-rule__params"
              >
                <div
                  v-for="param in row.params.filter(({ op }) => !row.json || op === 'remove')"
                  :key="param.key"
                  class="parameter-rule__param"
                  :class="{ 'parameter-rule__param--drop': param.op === 'remove' }"
                >
                  <!-- JSON 视图下 set 归 textarea，行只可能是删除，下拉就成了死控件。 -->
                  <span v-if="row.json" class="parameter-rule__op-static">{{
                    t('group.settings.parameterOverrides.opRemove')
                  }}</span>
                  <span v-else class="parameter-rule__op">
                    <AppSelect
                      :model-value="param.op"
                      :label="t('group.settings.parameterOverrides.paramAction')"
                      :options="opOptions"
                      :disabled="disabled"
                      size="compact"
                      @update:model-value="setParamOp(param, $event)"
                    />
                  </span>
                  <CompactFieldError
                    :id="`${instanceId}-path-${param.key}`"
                    class="parameter-rule__path-cell"
                    :error="errorsByRule.get(row.key)?.params.get(param.key)"
                  >
                    <template #default="{ describedBy, invalid }">
                      <input
                        :id="`${instanceId}-path-${param.key}`"
                        v-model="param.path"
                        class="parameter-rule__input parameter-rule__path"
                        :placeholder="
                          param.op === 'remove' ? 'generationConfig/topP' : 'thinking/type'
                        "
                        :aria-label="t('group.settings.parameterOverrides.paramPath')"
                        :aria-describedby="describedBy"
                        :aria-invalid="invalid"
                        :disabled="disabled"
                        spellcheck="false"
                      />
                    </template>
                  </CompactFieldError>
                  <template v-if="param.op === 'set'">
                    <span class="parameter-rule__kind">
                      <AppSelect
                        :model-value="param.kind"
                        :label="t('group.settings.parameterOverrides.paramKind')"
                        :options="kindOptions"
                        :disabled="disabled"
                        size="compact"
                        @update:model-value="setParamKind(param, $event)"
                      />
                    </span>
                    <!-- 值控件跟着类型走：布尔只有两个取值，空值没有值可填。 -->
                    <span
                      v-if="param.kind === 'null'"
                      class="parameter-rule__value-none"
                      aria-hidden="true"
                      >—</span
                    >
                    <span v-else-if="param.kind === 'boolean'" class="parameter-rule__bool">
                      <AppSelect
                        :model-value="param.valueText.trim() === 'false' ? 'false' : 'true'"
                        :label="t('group.settings.parameterOverrides.paramValue')"
                        :options="booleanOptions"
                        :disabled="disabled"
                        size="compact"
                        @update:model-value="param.valueText = $event"
                      />
                    </span>
                    <input
                      v-else
                      class="parameter-rule__input"
                      :value="param.valueText"
                      :placeholder="param.kind === 'json' ? '[&quot;\n\n&quot;]' : '0.7'"
                      :aria-label="t('group.settings.parameterOverrides.paramValue')"
                      :disabled="disabled"
                      spellcheck="false"
                      @input="param.valueText = ($event.target as HTMLInputElement).value"
                      @change="setParamValue(param, ($event.target as HTMLInputElement).value)"
                    />
                  </template>
                  <IconButton
                    variant="ghost"
                    size="compact"
                    :label="t('group.settings.parameterOverrides.deleteParam')"
                    :disabled="disabled"
                    @click="removeParam(row, param.key)"
                    ><X :size="15" aria-hidden="true"
                  /></IconButton>
                </div>
              </div>

              <InlineFeedback v-if="pathsCrossing(row)" tone="warning" appearance="ledger-hint">
                {{ t('group.settings.parameterOverrides.pathsCross') }}
              </InlineFeedback>

              <div class="parameter-rule__params-foot">
                <AppButton variant="link" size="inline" :disabled="disabled" @click="addParam(row)">
                  <Plus :size="14" aria-hidden="true" />
                  {{
                    row.json
                      ? t('group.settings.parameterOverrides.addRemovePath')
                      : t('group.settings.parameterOverrides.addParam')
                  }}
                </AppButton>
                <span class="parameter-rule__params-actions">
                  <AppButton
                    v-if="row.json"
                    variant="link"
                    size="inline"
                    :disabled="disabled || !row.setText.trim()"
                    @click="formatSetText(row)"
                    >{{ t('group.settings.parameterOverrides.formatJSON') }}</AppButton
                  >
                  <AppButton
                    variant="link"
                    size="inline"
                    :disabled="disabled || switchBlocked.get(row.key)"
                    @click="toggleJSON(row)"
                    >{{
                      row.json
                        ? t('group.settings.parameterOverrides.toRows')
                        : t('group.settings.parameterOverrides.toJSON')
                    }}</AppButton
                  >
                  <AppTooltip
                    :content="`${t('group.settings.parameterOverrides.pathHint')}　${t('group.settings.parameterOverrides.mergeHint')}`"
                  >
                    <button
                      class="parameter-rule__hint"
                      type="button"
                      :aria-label="t('group.settings.parameterOverrides.pathHint')"
                    >
                      ?
                    </button>
                  </AppTooltip>
                </span>
              </div>
            </div>
          </div>

          <InlineFeedback v-if="errorsByRule.get(row.key)?.action" tone="danger">
            {{ errorsByRule.get(row.key)?.action }}
          </InlineFeedback>
        </div>
      </article>
    </div>

    <datalist :id="`${instanceId}-models`">
      <option v-for="model in modelSuggestions" :key="model" :value="model" />
    </datalist>
  </div>
</template>

<style scoped>
.parameter-rules {
  display: grid;
  gap: 9px;
}
.parameter-rules__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
}
.parameter-rules__order {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.parameter-rules__empty {
  margin: 0;
  border: 1px dashed var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 12px 14px;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
}
.parameter-rules__list {
  display: grid;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.parameter-rule {
  border-top: 1px solid var(--color-border-subtle);
}
.parameter-rule:first-child {
  border-top: 0;
}
.parameter-rule--invalid {
  box-shadow: inset 2px 0 var(--color-danger);
}
.parameter-rule__head {
  display: flex;
  align-items: center;
  min-height: 42px;
}
.parameter-rule__summary {
  display: grid;
  min-width: 0;
  flex: 1;
  grid-template-columns: auto 18px auto minmax(0, 1fr);
  align-items: center;
  gap: var(--space-2);
  border: 0;
  background: transparent;
  color: inherit;
  padding: 5px 6px 5px 8px;
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.parameter-rule__head:hover {
  background: var(--color-interactive-hover);
}
.parameter-rule__chevron {
  color: var(--color-text-faint);
  transition: transform var(--duration-fast) var(--easing-standard);
}
.parameter-rule__summary[aria-expanded='true'] .parameter-rule__chevron {
  transform: rotate(180deg);
}
.parameter-rule__index {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  text-align: center;
}
.parameter-rule__match {
  display: flex;
  flex: none;
  align-items: center;
  gap: 5px;
}
.parameter-rule__tag,
.parameter-rule__chip {
  display: inline-flex;
  max-width: 100%;
  flex: none;
  align-items: center;
  gap: 5px;
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 3px 7px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  line-height: 1.35;
  white-space: nowrap;
}
.parameter-rule__tag {
  color: var(--color-action);
  font-weight: 620;
  background: var(--color-action-soft);
}
.parameter-rule__tag--any {
  border: 1px dashed var(--color-border-control);
  background: transparent;
  color: var(--color-text-faint);
  font-weight: 400;
}
/* 摘要尾部：chips 收缩并裁切，+N 常驻可见，两种视口共用这一套。 */
.parameter-rule__summary-tail {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}
.parameter-rule__chips {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: center;
  gap: 5px;
  overflow: hidden;
}
/* 单个 chip 设上限：一条很长的路径不该把同行其余摘要全挤出视野。
   只有路径收缩并省略，前缀与值始终完整 —— 截断值会让摘要变成误导。 */
.parameter-rule__chip {
  max-width: 250px;
}
.parameter-rule__chip b {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text);
  font-weight: 620;
  text-overflow: ellipsis;
}
.parameter-rule__chip-mark,
.parameter-rule__chip-value {
  flex: none;
}
.parameter-rule__chip-more {
  flex: none;
  margin-right: var(--space-1);
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}
.parameter-rule__chip--drop {
  background: color-mix(in srgb, var(--color-danger-bg) 72%, transparent);
  color: var(--color-danger);
}
.parameter-rule__tools {
  display: flex;
  flex: none;
  align-items: center;
  gap: 1px;
  padding-right: 7px;
}
/* 排序与删除之间留出空隙：误点下移和误点删除的代价不对称。 */
.parameter-rule__tools-gap {
  width: var(--space-3);
}
.parameter-rule__body {
  display: grid;
  gap: 11px;
  border-top: 1px solid var(--color-border-subtle);
  background: color-mix(in srgb, var(--color-surface-sunken) 42%, var(--color-surface));
  padding: 12px 13px 13px 31px;
}
.parameter-rule__field {
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr);
  align-items: start;
  gap: 10px;
}
.parameter-rule__label {
  padding-top: 8px;
  line-height: 1.3;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 640;
  letter-spacing: 0.04em;
}
.parameter-rule__field--params > .parameter-rule__label {
  padding-top: 12px;
}
.parameter-rule__match-inputs {
  display: grid;
  grid-template-columns: 168px minmax(0, 320px) auto;
  align-items: center;
  justify-content: start;
  gap: var(--space-2);
}
.parameter-rule__count {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.parameter-rule__count--zero {
  color: var(--color-warning);
}
.parameter-rule__input {
  width: 100%;
  min-width: 0;
  min-height: var(--control-compact);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 5px 8px;
  font: inherit;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.parameter-rule__textarea {
  min-height: 118px;
  line-height: var(--line-normal);
  resize: vertical;
  tab-size: 2;
}
.parameter-rule__params-stack {
  display: grid;
  min-width: 0;
  gap: 7px;
}
.parameter-rule__params {
  display: grid;
  gap: 1px;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-border-subtle);
}
/* 动作 / 路径 / 类型 / 值 / 删除。类型与值定宽，路径吃掉剩余空间。 */
.parameter-rule__param {
  display: grid;
  grid-template-columns: 74px minmax(0, 1fr) 86px 176px auto;
  align-items: center;
  gap: 6px;
  background: var(--color-surface);
  padding: 4px 5px 4px 6px;
}
/* 删除行沿用同一套列宽：路径框与设置行严格对齐，类型与值两列留空。
   列对齐是这张表的主要价值，不能因为少两个控件就让路径框变长。 */
.parameter-rule__param--drop .parameter-rule__path-cell {
  grid-column: 2;
}
.parameter-rule__param--drop > :last-child {
  grid-column: 5;
}
.parameter-rule__op :deep(.app-select__trigger),
.parameter-rule__kind :deep(.app-select__trigger),
.parameter-rule__bool :deep(.app-select__trigger) {
  width: 100%;
  border-color: transparent;
  background: var(--color-surface-sunken);
}
.parameter-rule__op :deep(.app-select__trigger),
.parameter-rule__kind :deep(.app-select__trigger) {
  font-size: var(--text-label-xs);
}
.parameter-rule__op :deep(.app-select__trigger):hover:not(:disabled),
.parameter-rule__kind :deep(.app-select__trigger):hover:not(:disabled),
.parameter-rule__bool :deep(.app-select__trigger):hover:not(:disabled) {
  border-color: var(--color-border-control);
}
.parameter-rule__value-none {
  display: inline-flex;
  min-height: var(--control-compact);
  align-items: center;
  color: var(--color-text-muted);
  padding-left: 7px;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
/* 一行里的下拉、输入框与图标按钮统一到 --control-compact，避免互相错位。
   表格单元格用填充式：静态靠底色表明可输入（白底表格上的浅灰格），
   不铺静态边框以免一行里挤出四道框；hover 补边框，focus 与错误再加深。 */
.parameter-rule__param .parameter-rule__input {
  border-color: transparent;
  background: var(--color-surface-sunken);
  padding: 4px 7px;
}
.parameter-rule__param .parameter-rule__input:hover:not(:disabled):not(:focus) {
  border-color: var(--color-border-control);
}
.parameter-rule__param .parameter-rule__input[aria-invalid='true'] {
  border-color: var(--color-danger);
}
/* AppSelect 的根是 Fragment，class 会落到 trigger 且丢掉 scope 属性，
   所以这三个下拉都自带包裹元素：定位与选择器才有可靠的落点。 */
.parameter-rule__op,
.parameter-rule__kind,
.parameter-rule__bool {
  display: block;
  min-width: 0;
}
.parameter-rule__param--drop .parameter-rule__op :deep(.app-select__trigger) {
  background: color-mix(in srgb, var(--color-danger-bg) 70%, transparent);
  color: var(--color-danger);
}
.parameter-rule__op-static {
  display: inline-flex;
  min-height: var(--control-compact);
  align-items: center;
  border-radius: var(--radius-tag);
  background: color-mix(in srgb, var(--color-danger-bg) 70%, transparent);
  color: var(--color-danger);
  padding: 0 7px;
  font-size: var(--text-label-xs);
  font-weight: 640;
}
.parameter-rule__params-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  flex-wrap: wrap;
}
.parameter-rule__params-actions {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2-5);
}
.parameter-rule__hint {
  display: inline-grid;
  width: 15px;
  height: 15px;
  place-items: center;
  border: 1px solid var(--color-border-control);
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-faint);
  font-size: 9.5px;
  font-weight: 700;
  line-height: 1;
  cursor: help;
}
.parameter-rule__hint:hover {
  border-color: var(--color-action);
  color: var(--color-action);
}
@media (max-width: 800px) {
  .parameter-rules__bar {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .parameter-rule__head {
    align-items: stretch;
    flex-direction: column;
  }
  .parameter-rule__summary {
    min-height: var(--touch-target);
    grid-template-columns: auto 18px minmax(0, 1fr);
    align-items: center;
    row-gap: 5px;
  }
  .parameter-rule__match {
    grid-area: 1 / 3;
    flex-wrap: wrap;
  }
  .parameter-rule__summary-tail {
    grid-area: 2 / 3;
  }
  /* 独占一行时两端分布：填满行宽，且排序与删除相距最远。 */
  .parameter-rule__tools {
    justify-content: space-between;
    border-top: 1px solid var(--color-border-subtle);
    padding: 5px 7px;
  }
  .parameter-rule__tools-gap {
    display: none;
  }
  .parameter-rule__tools :deep(.icon-button) {
    width: var(--touch-target);
    height: var(--touch-target);
  }
  .parameter-rule__body {
    padding-left: 13px;
  }
  .parameter-rule__field {
    grid-template-columns: 1fr;
    gap: 5px;
  }
  .parameter-rule__label {
    padding-top: 0;
  }
  .parameter-rule__field--params > .parameter-rule__label {
    padding-top: 0;
  }
  .parameter-rule__match-inputs {
    grid-template-columns: 1fr;
  }
  /* 窄屏折成两行：动作与路径在上，类型与值缩进到路径左缘对齐，
     值不会退回容器左边缘而与键名失去关联。删除行只有上面一行。 */
  .parameter-rule__param {
    grid-template-columns: 72px 84px minmax(0, 1fr) var(--control-compact);
    row-gap: 5px;
  }
  .parameter-rule__op,
  .parameter-rule__op-static {
    grid-area: 1 / 1;
  }
  .parameter-rule__param .parameter-rule__path-cell {
    grid-area: 1 / 2 / 2 / 4;
  }
  .parameter-rule__kind {
    grid-area: 2 / 2;
  }
  .parameter-rule__bool,
  .parameter-rule__value-none,
  .parameter-rule__param > .parameter-rule__input:not(.parameter-rule__path) {
    grid-area: 2 / 3;
  }
  .parameter-rule__param > :last-child {
    grid-area: 1 / 4;
  }
  .parameter-rule__textarea {
    min-height: 160px;
    font-size: 16px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .parameter-rule__chevron {
    transition: none;
  }
}
</style>
