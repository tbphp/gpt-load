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
import FormField from '@/components/ui/FormField.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import {
  assertJSONNumbersRoundTrip,
  isJSONSafeNumber,
  JSONNumberPrecisionError,
} from '@/lib/json-number'

interface RemoveRow {
  key: number
  path: string
}

interface RuleRow {
  key: number
  open: boolean
  protocol: string
  model: string
  setText: string
  remove: RemoveRow[]
}

interface RuleErrors {
  model?: string
  set?: string
  action?: string
  remove: Map<number, string>
}

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

function cloneJSONValue(value: unknown): ParameterJSONValue {
  if (Array.isArray(value)) return value.map(cloneJSONValue)
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value).map(([key, nested]) => [key, cloneJSONValue(nested)]),
    )
  }
  if (typeof value === 'number') {
    if (!isJSONSafeNumber(value)) throw new JSONNumberPrecisionError()
    return value
  }
  if (value === null || typeof value === 'boolean' || typeof value === 'string') return value
  throw new TypeError('invalid JSON value')
}

function createRows(value: ParameterOverrideRuleDto[]): RuleRow[] {
  return value.map((rule) => ({
    key: newKey(),
    open: false,
    protocol: rule.match.protocol ?? '',
    model: rule.match.model ?? '',
    setText: rule.set ? JSON.stringify(rule.set, null, 2) : '',
    remove: (rule.remove ?? []).map((path) => ({ key: newKey(), path })),
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
  ...availableProtocols.value.map((value) => ({
    value,
    label: value,
  })),
])

function parseSet(text: string): Record<string, ParameterJSONValue> | undefined {
  const trimmed = text.trim()
  if (!trimmed) return undefined
  const value: unknown = JSON.parse(trimmed)
  assertJSONNumbersRoundTrip(trimmed)
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return undefined
  return Object.fromEntries(
    Object.entries(value as Record<string, ParameterJSONValue>).map(([key, nested]) => [
      key,
      cloneJSONValue(nested),
    ]),
  )
}

function decodePointerRoot(path: string): string | undefined {
  if (!path.startsWith('/')) return undefined
  const segments = path.slice(1).split('/')
  let root = ''
  for (const [index, raw] of segments.entries()) {
    if (/~(?![01])/u.test(raw)) return undefined
    const decoded = raw.replaceAll('~1', '/').replaceAll('~0', '~')
    if (decoded === '-' || /^\d+$/u.test(decoded)) return undefined
    if (index === 0) root = decoded
  }
  return root
}

function ruleErrors(row: RuleRow): RuleErrors {
  const errors: RuleErrors = { remove: new Map() }
  const model = row.model.trim()
  if (model.includes('*') && !/^[^*]+\*$/u.test(model)) {
    errors.model = t('group.settings.parameterOverrides.errors.modelPattern')
  }

  let set: Record<string, ParameterJSONValue> | undefined
  if (row.setText.trim()) {
    try {
      set = parseSet(row.setText)
      if (!set) errors.set = t('group.settings.parameterOverrides.errors.setObject')
      else if (
        Object.keys(set).some((key) => ['model', 'stream', 'store'].includes(key.toLowerCase()))
      )
        errors.set = t('group.settings.parameterOverrides.errors.forbiddenField')
    } catch (cause: unknown) {
      errors.set = t(
        cause instanceof JSONNumberPrecisionError
          ? 'group.settings.parameterOverrides.errors.unsafeNumber'
          : 'group.settings.parameterOverrides.errors.invalidJSON',
      )
    }
  }

  const seen = new Set<string>()
  for (const item of row.remove) {
    const root = decodePointerRoot(item.path)
    let error = ''
    if (!item.path) error = t('group.settings.parameterOverrides.errors.removeRequired')
    else if (root === undefined) error = t('group.settings.parameterOverrides.errors.removePointer')
    else if (['model', 'stream', 'store'].includes(root.toLowerCase()))
      error = t('group.settings.parameterOverrides.errors.forbiddenField')
    else if (seen.has(item.path))
      error = t('group.settings.parameterOverrides.errors.removeDuplicate')
    seen.add(item.path)
    if (error) errors.remove.set(item.key, error)
  }

  if ((!set || Object.keys(set).length === 0) && row.remove.length === 0) {
    errors.action = t('group.settings.parameterOverrides.errors.actionRequired')
  }
  return errors
}

const errorsByRule = computed(
  () => new Map(rows.value.map((row) => [row.key, ruleErrors(row)] as const)),
)
const valid = computed(() =>
  [...errorsByRule.value.values()].every(
    (errors) => !errors.model && !errors.set && !errors.action && errors.remove.size === 0,
  ),
)

function serializeRows(): ParameterOverrideRuleDto[] {
  return rows.value.map((row) => {
    const protocol = row.protocol as AccessProtocol
    const model = row.model.trim()
    const set = parseSet(row.setText)
    return {
      match: {
        ...(protocol ? { protocol } : {}),
        ...(model ? { model } : {}),
      },
      ...(set && Object.keys(set).length > 0 ? { set } : {}),
      ...(row.remove.length > 0 ? { remove: row.remove.map(({ path }) => path) } : {}),
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
    setText: '{\n  \n}',
    remove: [],
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
    setText: source.setText,
    remove: source.remove.map(({ path }) => ({ key: newKey(), path })),
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

function addRemovePath(row: RuleRow): void {
  row.remove.push({ key: newKey(), path: '' })
}

function formatSet(row: RuleRow): void {
  try {
    const set = parseSet(row.setText)
    if (set) row.setText = JSON.stringify(set, null, 2)
  } catch {
    // 统一由输入框旁的校验信息提示错误。
  }
}

function protocolLabel(value: string): string {
  return value || t('group.settings.parameterOverrides.allProtocols')
}

function modelLabel(row: RuleRow): string {
  return row.model.trim() || t('group.settings.parameterOverrides.allModels')
}

function setCount(row: RuleRow): number {
  try {
    return Object.keys(parseSet(row.setText) ?? {}).length
  } catch {
    return 0
  }
}

function matchCount(model: string): number | undefined {
  const value = model.trim()
  if (!value || (value.includes('*') && !/^[^*]+\*$/u.test(value))) return undefined
  const prefix = value.endsWith('*') ? value.slice(0, -1) : undefined
  return modelSuggestions.value.filter((candidate) =>
    prefix === undefined ? candidate === value : candidate.startsWith(prefix),
  ).length
}
</script>

<template>
  <div class="parameter-rules">
    <div class="parameter-rules__intro">
      <p>{{ t('group.settings.parameterOverrides.orderHelp') }}</p>
      <AppButton size="compact" :disabled="disabled" @click="addRule">
        <Plus :size="15" aria-hidden="true" />
        {{ t('group.settings.parameterOverrides.addRule') }}
      </AppButton>
    </div>

    <p class="sr-only" aria-live="polite">{{ moveAnnouncement }}</p>

    <div v-if="rows.length === 0" class="parameter-rules__empty">
      <strong>{{ t('group.settings.parameterOverrides.emptyTitle') }}</strong>
      <span>{{ t('group.settings.parameterOverrides.emptyDescription') }}</span>
    </div>

    <article
      v-for="(row, index) in rows"
      :key="row.key"
      class="parameter-rule"
      :class="{
        'parameter-rule--invalid':
          errorsByRule.get(row.key)?.action ||
          errorsByRule.get(row.key)?.model ||
          errorsByRule.get(row.key)?.set ||
          errorsByRule.get(row.key)?.remove.size,
      }"
    >
      <header class="parameter-rule__header">
        <button
          type="button"
          class="parameter-rule__summary"
          :aria-expanded="row.open"
          :aria-controls="`${instanceId}-rule-${row.key}`"
          @click="row.open = !row.open"
        >
          <ChevronDown :size="16" aria-hidden="true" />
          <span class="parameter-rule__identity">
            <strong>{{
              t('group.settings.parameterOverrides.rule', { position: index + 1 })
            }}</strong>
            <span class="parameter-rule__match">
              <code>{{ protocolLabel(row.protocol) }}</code>
              <span aria-hidden="true">·</span>
              <small>{{ modelLabel(row) }}</small>
            </span>
          </span>
          <span class="parameter-rule__counts">
            {{
              t('group.settings.parameterOverrides.summary', {
                set: setCount(row),
                remove: row.remove.length,
              })
            }}
          </span>
        </button>
        <div class="parameter-rule__actions">
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
      </header>

      <div v-if="row.open" :id="`${instanceId}-rule-${row.key}`" class="parameter-rule__body">
        <fieldset class="parameter-rule__conditions">
          <legend>{{ t('group.settings.parameterOverrides.conditions') }}</legend>
          <div class="parameter-rule__condition-grid">
            <FormField
              :id="`${instanceId}-protocol-${row.key}`"
              :label="t('group.settings.parameterOverrides.protocol')"
              :description="t('group.settings.parameterOverrides.protocolHelp')"
              size="compact"
            >
              <AppSelect
                :id="`${instanceId}-protocol-${row.key}`"
                v-model="row.protocol"
                :label="t('group.settings.parameterOverrides.protocol')"
                :options="protocolOptions"
                :disabled="disabled"
                size="sm"
              />
            </FormField>
            <FormField
              :id="`${instanceId}-model-${row.key}`"
              :label="t('group.settings.parameterOverrides.model')"
              :description="t('group.settings.parameterOverrides.modelHelp')"
              :description-warning="
                matchCount(row.model) === 0
                  ? t('group.settings.parameterOverrides.modelNoMatch')
                  : undefined
              "
              :error="errorsByRule.get(row.key)?.model"
              size="compact"
            >
              <template #default="{ describedBy, invalid }">
                <input
                  :id="`${instanceId}-model-${row.key}`"
                  v-model="row.model"
                  :list="`${instanceId}-models`"
                  :placeholder="t('group.settings.parameterOverrides.allModels')"
                  :aria-describedby="describedBy"
                  :aria-invalid="invalid"
                  :disabled="disabled"
                  autocomplete="off"
                />
              </template>
            </FormField>
          </div>
          <InlineFeedback
            v-if="!row.protocol && protocols.length > 1"
            tone="warning"
            appearance="ledger-hint"
            >{{ t('group.settings.parameterOverrides.allProtocolWarning') }}</InlineFeedback
          >
        </fieldset>

        <div class="parameter-rule__values">
          <div class="parameter-rule__set">
            <FormField
              :id="`${instanceId}-set-${row.key}`"
              :label="t('group.settings.parameterOverrides.set')"
              :description="t('group.settings.parameterOverrides.setHelp')"
              :error="errorsByRule.get(row.key)?.set"
              size="compact"
            >
              <template #default="{ describedBy, invalid }">
                <textarea
                  :id="`${instanceId}-set-${row.key}`"
                  v-model="row.setText"
                  :placeholder="t('group.settings.parameterOverrides.setPlaceholder')"
                  :aria-describedby="describedBy"
                  :aria-invalid="invalid"
                  :disabled="disabled"
                  spellcheck="false"
                ></textarea>
              </template>
            </FormField>
            <AppButton
              variant="link"
              size="inline"
              :disabled="disabled || !row.setText.trim()"
              @click="formatSet(row)"
              >{{ t('group.settings.parameterOverrides.formatJSON') }}</AppButton
            >
          </div>

          <div class="parameter-rule__remove">
            <div class="parameter-rule__remove-heading">
              <span>
                <strong>{{ t('group.settings.parameterOverrides.remove') }}</strong>
                <small>{{ t('group.settings.parameterOverrides.removeHelp') }}</small>
              </span>
              <AppButton
                variant="link"
                size="inline"
                :disabled="disabled"
                @click="addRemovePath(row)"
                ><Plus :size="14" aria-hidden="true" />{{
                  t('group.settings.parameterOverrides.addRemove')
                }}</AppButton
              >
            </div>
            <div v-for="item in row.remove" :key="item.key" class="parameter-rule__remove-row">
              <FormField
                :id="`${instanceId}-remove-${item.key}`"
                :label="t('group.settings.parameterOverrides.removePath')"
                :label-hidden="true"
                :error="errorsByRule.get(row.key)?.remove.get(item.key)"
                size="compact"
              >
                <template #default="{ describedBy, invalid }">
                  <input
                    :id="`${instanceId}-remove-${item.key}`"
                    v-model="item.path"
                    placeholder="/generationConfig/topP"
                    :aria-describedby="describedBy"
                    :aria-invalid="invalid"
                    :disabled="disabled"
                    spellcheck="false"
                  />
                </template>
              </FormField>
              <IconButton
                variant="ghost"
                size="compact"
                :label="t('group.settings.parameterOverrides.deleteRemove')"
                :disabled="disabled"
                @click="row.remove = row.remove.filter(({ key }) => key !== item.key)"
                ><X :size="16" aria-hidden="true"
              /></IconButton>
            </div>
          </div>
        </div>

        <InlineFeedback v-if="errorsByRule.get(row.key)?.action" tone="danger">
          {{ errorsByRule.get(row.key)?.action }}
        </InlineFeedback>
      </div>
    </article>

    <datalist :id="`${instanceId}-models`">
      <option v-for="model in modelSuggestions" :key="model" :value="model" />
    </datalist>
  </div>
</template>

<style scoped>
.parameter-rules {
  display: grid;
  gap: 11px;
}
.parameter-rules__intro,
.parameter-rule__header,
.parameter-rule__remove-heading,
.parameter-rule__remove-row {
  display: flex;
  align-items: center;
}
.parameter-rules__intro {
  justify-content: space-between;
  gap: var(--space-4);
}
.parameter-rules__intro p {
  max-width: 620px;
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
  line-height: var(--line-normal);
}
.parameter-rules__empty {
  display: grid;
  gap: 3px;
  border: 1px dashed var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 14px 15px;
  color: var(--color-text-muted);
}
.parameter-rules__empty span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.parameter-rule {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.parameter-rule--invalid {
  box-shadow: inset 2px 0 var(--color-danger);
}
.parameter-rule__header {
  min-height: 50px;
}
.parameter-rule__summary {
  display: grid;
  min-width: 0;
  flex: 1;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-2);
  border: 0;
  background: transparent;
  color: inherit;
  padding: 10px 8px 10px 12px;
  text-align: left;
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--easing-standard);
}
.parameter-rule__summary:hover {
  background: var(--color-surface-sunken);
}
.parameter-rule__summary > svg {
  color: var(--color-text-faint);
  transition: transform var(--duration-fast) var(--easing-standard);
}
.parameter-rule__summary[aria-expanded='true'] > svg {
  transform: rotate(180deg);
}
.parameter-rule__identity,
.parameter-rule__match {
  display: flex;
  min-width: 0;
  align-items: center;
}
.parameter-rule__identity {
  gap: var(--space-2);
}
.parameter-rule__identity > strong {
  flex: none;
  font-size: var(--text-meta);
}
.parameter-rule__match {
  flex: 1;
  overflow: hidden;
  gap: 6px;
  color: var(--color-text-faint);
}
.parameter-rule__match code {
  flex: none;
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 3px 6px;
  font-family: var(--font-mono);
  font-size: 10.5px;
  line-height: 1.3;
}
.parameter-rule__match small,
.parameter-rule__remove-heading small {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.parameter-rule__counts {
  color: var(--color-text-faint);
  font-size: 10.5px;
  white-space: nowrap;
}
.parameter-rule__actions {
  display: flex;
  flex: none;
  gap: 1px;
  padding-right: 8px;
}
.parameter-rule__body {
  display: grid;
  gap: 15px;
  border-top: 1px solid var(--color-border-subtle);
  background: color-mix(in srgb, var(--color-surface-sunken) 42%, var(--color-surface));
  padding: 14px 16px 16px;
}
.parameter-rule__conditions {
  display: grid;
  gap: 10px;
  min-width: 0;
  margin: 0;
  border: 0;
  padding: 0;
}
.parameter-rule__conditions legend {
  margin-bottom: 9px;
  font-size: var(--text-meta);
  font-weight: 650;
}
.parameter-rule__condition-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}
.parameter-rule__condition-grid :deep(.app-select__trigger) {
  width: 100%;
}
.parameter-rule__condition-grid :deep(.app-select__value),
.parameter-rule__condition-grid input {
  font-family: var(--font-mono);
}
.parameter-rule__values {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(250px, 0.75fr);
  align-items: start;
  gap: 18px;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 15px;
}
.parameter-rule__set {
  display: grid;
  min-width: 0;
  gap: 6px;
}
.parameter-rule__values textarea {
  min-height: 132px;
  font-family: var(--font-mono);
  font-size: 13px;
  line-height: 1.55;
  tab-size: 2;
}
.parameter-rule__set > .app-button {
  justify-self: end;
}
.parameter-rule__remove {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: var(--space-2);
  border-left: 1px solid var(--color-border-subtle);
  padding-left: 18px;
}
.parameter-rule__remove-heading {
  justify-content: space-between;
  gap: var(--space-3);
}
.parameter-rule__remove-heading > span {
  display: grid;
  gap: 2px;
}
.parameter-rule__remove-heading strong {
  font-size: var(--text-sm);
  font-weight: 560;
}
.parameter-rule__remove-row {
  align-items: start;
  gap: var(--space-2);
}
.parameter-rule__remove-row > :first-child {
  min-width: 0;
  flex: 1;
}
.parameter-rule__remove-row input {
  font-family: var(--font-mono);
  font-size: 13px;
}
@media (max-width: 960px) {
  .parameter-rule__values {
    grid-template-columns: 1fr;
  }
  .parameter-rule__remove {
    border-top: 1px solid var(--color-border-subtle);
    border-left: 0;
    padding-top: 14px;
    padding-left: 0;
  }
}
@media (max-width: 800px) {
  .parameter-rules__intro,
  .parameter-rule__remove-heading {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .parameter-rule__condition-grid {
    grid-template-columns: 1fr;
  }
  .parameter-rule__header {
    align-items: stretch;
    flex-direction: column;
  }
  .parameter-rule__summary {
    min-height: var(--touch-target);
  }
  .parameter-rule__identity {
    display: grid;
    gap: 2px;
  }
  .parameter-rule__counts {
    display: none;
  }
  .parameter-rule__actions {
    justify-content: flex-end;
    border-top: 1px solid var(--color-border-subtle);
    padding: 5px 7px;
  }
  .parameter-rule__actions :deep(.icon-button) {
    width: var(--touch-target);
    height: var(--touch-target);
  }
  .parameter-rule__set > :deep(.app-button--inline),
  .parameter-rule__remove-heading > :deep(.app-button--inline) {
    min-height: var(--touch-target);
  }
  .parameter-rule__values textarea {
    min-height: 180px;
    font-size: 16px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .parameter-rule__summary,
  .parameter-rule__summary > svg {
    transition: none;
  }
}
</style>
