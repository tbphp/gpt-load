<script setup lang="ts">
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, Copy, Trash2 } from '@lucide/vue'
import { computed, nextTick, onScopeDispose, ref, toRaw, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  errorRuleDraft,
  errorRuleValue,
  errorRuleErrors,
  errorRuleEffects,
  hasErrorRuleCooldown,
  validErrorRules,
  type ErrorRule,
  type ErrorRuleDraft,
} from '@shared/error-rules'
import {
  AppBadge,
  AppButton,
  AppField,
  AppIconButton,
  AppNotice,
  AppOverflowText,
  AppRulesEmpty,
  AppSelect,
  AppSwitch,
  AppTag,
  AppTextField,
} from '@modern/components/ui'

const props = defineProps<{
  modelValue: ErrorRule[]
  disabled?: boolean
  readonly?: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: ErrorRule[]]
  'update:valid': [value: boolean]
  'update:pending': [value: boolean]
}>()
const { t, n } = useI18n()
const instanceId = useId()
let nextKey = 0
const rows = ref(props.modelValue.map((rule) => errorRuleDraft(rule, nextKey++)))
const elements = new Map<number, HTMLElement>()
const announcement = ref('')
const attempted = ref(false)
const inactive = computed(() => props.disabled || props.readonly)
const canAdd = computed(() => !inactive.value && rows.value.length < 100)
const values = computed(() => rows.value.map(errorRuleValue))
const errors = computed(() => new Map(rows.value.map((row) => [row.key, errorRuleErrors(row)])))
const valid = computed(
  () => !rows.value.some((row) => row.statusInvalid) && validErrorRules(values.value),
)
const hasPendingInput = computed(() =>
  rows.value.some((row) => row.statusInput.trim() || row.keywordInput.trim()),
)
let synchronized = JSON.stringify(values.value)
let emitted = JSON.stringify(props.modelValue)
let emittedValue = toRaw(props.modelValue)

watch(valid, (value) => emit('update:valid', value), { immediate: true })
watch(
  values,
  (value) => {
    const encoded = JSON.stringify(value)
    if (encoded === synchronized) return
    synchronized = encoded
    emitted = encoded
    emittedValue = toRaw(value)
    emit('update:modelValue', value)
  },
  { deep: true },
)
watch(
  () => props.modelValue,
  (value) => {
    const encoded = JSON.stringify(value)
    if (encoded === emitted && toRaw(value) === emittedValue) return
    emitted = encoded
    emittedValue = toRaw(value)
    const next = value.map((rule) => errorRuleDraft(rule, nextKey++))
    synchronized = JSON.stringify(next.map(errorRuleValue))
    rows.value = next
    attempted.value = false
  },
  { deep: true },
)
watch(hasPendingInput, (value) => emit('update:pending', value), { immediate: true })
onScopeDispose(() => emit('update:pending', false))

const effects = computed(() =>
  errorRuleEffects.map((value) => ({ value, label: t('errorRules.effects.' + value) })),
)

function rowRef(key: number, element: unknown): void {
  if (element instanceof HTMLElement) elements.set(key, element)
  else elements.delete(key)
}
async function focusRule(row: ErrorRuleDraft): Promise<void> {
  row.open = true
  await nextTick()
  const element = elements.get(row.key)
  element?.scrollIntoView({ block: 'nearest' })
  const target =
    element?.querySelector<HTMLElement>('[aria-invalid="true"]') ??
    element?.querySelector<HTMLElement>('input, textarea')
  target?.focus({ preventScroll: true })
}
function add(): void {
  if (inactive.value || rows.value.length >= 100) return
  const row = errorRuleDraft({ retry: 'none', effect: 'none' }, nextKey++, true)
  rows.value.push(row)
  void focusRule(row)
}
function copy(index: number): void {
  const source = rows.value[index]
  if (!source || inactive.value || rows.value.length >= 100) return
  const row = {
    ...source,
    key: nextKey++,
    open: true,
    statusInput: '',
    statusInvalid: false,
    keywordInput: '',
  }
  rows.value.splice(index + 1, 0, row)
  void focusRule(row)
}
function move(index: number, offset: -1 | 1): void {
  const target = index + offset
  if (inactive.value || target < 0 || target >= rows.value.length) return
  const [row] = rows.value.splice(index, 1)
  if (!row) return
  rows.value.splice(target, 0, row)
  announcement.value = t('errorRules.moved', { number: n(target + 1) })
}
function remove(index: number): void {
  if (!inactive.value) rows.value.splice(index, 1)
}
function selectedStatuses(row: ErrorRuleDraft): number[] {
  return errorRuleValue(row).status_codes ?? []
}
function addStatus(row: ErrorRuleDraft, event?: KeyboardEvent): void {
  if (event?.isComposing || event?.keyCode === 229) return
  event?.preventDefault()
  event?.stopPropagation()
  if (inactive.value) return
  const value = row.statusInput.trim()
  if (!value) {
    row.statusInput = ''
    row.statusInvalid = false
    return
  }
  const code = Number(value)
  if (!/^\d{3}$/u.test(value) || code < 200 || code > 599) {
    row.statusInvalid = true
    return
  }
  const current = selectedStatuses(row)
  if (!current.includes(code)) row.statuses = [...current, code].join(', ')
  row.statusInput = ''
  row.statusInvalid = false
}
function removeStatus(row: ErrorRuleDraft, value: number): void {
  if (!inactive.value)
    row.statuses = selectedStatuses(row)
      .filter((code) => code !== value)
      .join(', ')
}
function keywords(row: ErrorRuleDraft): string[] {
  return errorRuleValue(row).keywords ?? []
}
function addKeyword(row: ErrorRuleDraft, event?: KeyboardEvent): void {
  if (event?.isComposing || event?.keyCode === 229) return
  event?.preventDefault()
  event?.stopPropagation()
  if (inactive.value) return
  const value = row.keywordInput.trim()
  const current = keywords(row)
  if (value && !current.some((keyword) => keyword.toLowerCase() === value.toLowerCase()))
    row.keywords = [...current, value].join('\n')
  row.keywordInput = ''
}
function removeKeyword(row: ErrorRuleDraft, value: string): void {
  if (!inactive.value)
    row.keywords = keywords(row)
      .filter((keyword) => keyword !== value)
      .join('\n')
}
function matchSummary(row: ErrorRuleDraft): string {
  const value = errorRuleValue(row)
  const parts = [
    value.status_codes?.length ? 'HTTP ' + value.status_codes.join(', ') : '',
    value.keywords?.join(' / ') ?? '',
  ]
  return parts.filter(Boolean).join(' · ') || t('errorRules.addConditions')
}
function effectSummary(row: ErrorRuleDraft): string {
  const effect = t('errorRules.effects.' + row.effect)
  return hasErrorRuleCooldown(row.effect) && row.cooldown
    ? effect + ' · ' + t('errorRules.duration', { seconds: row.cooldown })
    : effect
}
function rowInvalid(index: number): boolean {
  return rows.value[index]!.statusInvalid || !validErrorRules([values.value[index]!])
}
async function focusFirstInvalid(): Promise<void> {
  const row = rows.value.find((_, index) => rowInvalid(index))
  if (row) await focusRule(row)
}
async function prepareSave(): Promise<boolean> {
  if (inactive.value) return true
  attempted.value = true
  for (const row of rows.value) {
    addStatus(row)
    addKeyword(row)
  }
  await nextTick()
  if (valid.value) return true
  await focusFirstInvalid()
  return false
}
defineExpose({ add, canAdd, focusFirstInvalid, prepareSave })
</script>

<template>
  <div class="modern-error-editor">
    <p class="modern-sr-only" role="status">{{ announcement }}</p>
    <AppRulesEmpty v-if="!rows.length" />
    <article
      v-for="(row, index) in rows"
      :key="row.key"
      :ref="(element) => rowRef(row.key, element)"
      class="modern-error-rule"
      :class="{ 'is-invalid': rowInvalid(index) && !row.open }"
    >
      <header class="modern-error-heading">
        <AppButton
          class="modern-error-toggle"
          variant="ghost"
          size="xs"
          :icon="row.open ? ChevronDown : ChevronRight"
          :aria-expanded="row.open"
          :aria-controls="instanceId + '-' + row.key"
          @click="row.open = !row.open"
        >
          <span class="modern-error-number">{{
            t('errorRules.rule', { number: n(index + 1) })
          }}</span>
          <AppOverflowText :text="matchSummary(row)" />
        </AppButton>
        <div v-if="!readonly" class="modern-error-tools">
          <div class="modern-error-move">
            <AppIconButton
              :icon="ArrowUp"
              :label="t('errorRules.up')"
              size="xs"
              :disabled="disabled || index === 0"
              @click="move(index, -1)"
            />
            <AppIconButton
              :icon="ArrowDown"
              :label="t('errorRules.down')"
              size="xs"
              :disabled="disabled || index === rows.length - 1"
              @click="move(index, 1)"
            />
          </div>
          <AppIconButton
            :icon="Copy"
            :label="t('errorRules.copy')"
            size="xs"
            :disabled="disabled || rows.length >= 100"
            @click="copy(index)"
          />
          <AppIconButton
            :icon="Trash2"
            :label="t('errorRules.remove')"
            size="xs"
            :disabled="disabled"
            @click="remove(index)"
          />
        </div>
      </header>
      <div v-if="!row.open" :id="instanceId + '-' + row.key" class="modern-error-summary">
        <AppBadge v-if="rowInvalid(index)" tone="warning" variant="plain" size="xs">
          {{ t('errorRules.incomplete') }}
        </AppBadge>
        <AppTag :text="t('errorRules.retries.' + row.retry)" tone="neutral" size="xs" />
        <AppTag :text="effectSummary(row)" tone="neutral" size="xs" />
      </div>
      <div v-else :id="instanceId + '-' + row.key" class="modern-error-body">
        <div class="modern-error-match">
          <div class="modern-error-condition">
            <AppTextField
              v-model="row.statusInput"
              :label="t('errorRules.statuses')"
              :placeholder="t('errorRules.statusesPlaceholder')"
              inputmode="numeric"
              size="xs"
              :disabled="inactive"
              :error="
                errors.get(row.key)?.statuses
                  ? t('errorRules.errors.statuses')
                  : attempted && errors.get(row.key)?.condition
                    ? t('errorRules.errors.condition')
                    : undefined
              "
              autocomplete="off"
              @update:model-value="row.statusInvalid = false"
              @keydown.enter="addStatus(row, $event)"
            />
            <div v-if="selectedStatuses(row).length" class="modern-error-tags">
              <AppTag
                v-for="code in selectedStatuses(row)"
                :key="code"
                :text="String(code)"
                size="xs"
                variant="outline"
                :removable="!readonly"
                :disabled="disabled"
                @remove="removeStatus(row, code)"
              />
            </div>
          </div>
          <div class="modern-error-condition">
            <AppTextField
              v-model="row.keywordInput"
              :label="t('errorRules.keywords')"
              :placeholder="t('errorRules.keywordsPlaceholder')"
              size="xs"
              :disabled="inactive"
              autocomplete="off"
              spellcheck="false"
              @keydown.enter="addKeyword(row, $event)"
            />
            <div v-if="keywords(row).length" class="modern-error-tags">
              <AppTag
                v-for="keyword in keywords(row)"
                :key="keyword"
                :text="keyword"
                size="xs"
                variant="outline"
                :removable="!readonly"
                :disabled="disabled"
                @remove="removeKeyword(row, keyword)"
              />
            </div>
          </div>
        </div>
        <div class="modern-error-actions">
          <AppField
            :id="instanceId + '-retry-' + row.key"
            :label="t('errorRules.retry')"
            :disabled="inactive"
          >
            <AppSwitch
              :id="instanceId + '-retry-' + row.key"
              :model-value="row.retry === 'next_candidate'"
              :label="t('errorRules.retry')"
              :disabled="inactive"
              size="sm"
              @update:model-value="row.retry = $event ? 'next_candidate' : 'none'"
            />
          </AppField>
          <div
            class="modern-error-effects"
            :class="{ 'has-cooldown': hasErrorRuleCooldown(row.effect) }"
          >
            <AppSelect
              v-model="row.effect"
              :label="t('errorRules.effect')"
              :options="effects"
              size="xs"
              :disabled="inactive"
            />
            <AppTextField
              v-if="hasErrorRuleCooldown(row.effect)"
              :model-value="row.cooldown"
              :label="t('errorRules.cooldown')"
              :placeholder="t('errorRules.cooldownPlaceholder')"
              inputmode="numeric"
              size="xs"
              :disabled="inactive"
              :error="errors.get(row.key)?.cooldown ? t('errorRules.errors.cooldown') : undefined"
              @update:model-value="row.cooldown = $event"
            />
          </div>
        </div>
      </div>
    </article>
    <AppNotice
      v-if="!valid && rows.every((row) => !Object.keys(errorRuleErrors(row)).length)"
      tone="warning"
      >{{ t('errorRules.errors.invalid') }}</AppNotice
    >
  </div>
</template>

<style scoped>
.modern-error-editor {
  container: modern-error-editor / inline-size;
  display: grid;
  min-width: 0;
  gap: var(--modern-space-2);
}
.modern-error-heading,
.modern-error-tools,
.modern-error-move {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-error-rule {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
}
.modern-error-rule.is-invalid {
  border-color: color-mix(in srgb, var(--modern-danger) 35%, var(--modern-border));
}
.modern-error-heading {
  padding: var(--modern-space-1);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
.modern-error-toggle {
  flex: 1;
  min-width: 0;
  justify-content: flex-start;
  text-align: left;
}
.modern-error-number {
  flex: none;
  color: var(--modern-text);
}
.modern-error-toggle > span:last-child {
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
}
.modern-error-tools,
.modern-error-move {
  flex: none;
  gap: 0;
}
.modern-error-move {
  margin-right: var(--modern-space-1);
  padding-right: var(--modern-space-1);
  border-right: var(--modern-line-width) solid var(--modern-border);
}
.modern-error-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1-5);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-error-body {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3);
}
.modern-error-match,
.modern-error-actions,
.modern-error-effects {
  display: grid;
  align-items: start;
  min-width: 0;
  gap: var(--modern-space-3);
}
.modern-error-match {
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.4fr);
}
.modern-error-actions {
  grid-template-columns: 5rem minmax(0, 1fr);
}
.modern-error-effects {
  grid-template-columns: minmax(0, 1fr);
}
.modern-error-condition {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1-5);
}
.modern-error-tags {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  min-width: 0;
  gap: var(--modern-space-1-5);
}
.modern-error-effects.has-cooldown {
  grid-template-columns: minmax(0, 1fr) 14rem;
}
@container modern-error-editor (max-width: 640px) {
  .modern-error-effects.has-cooldown {
    grid-template-columns: minmax(0, 1fr);
  }
}
@container modern-error-editor (max-width: 440px) {
  .modern-error-heading {
    flex-wrap: wrap;
  }
  .modern-error-toggle {
    flex-basis: 100%;
  }
  .modern-error-tools {
    margin-left: auto;
  }
  .modern-error-match,
  .modern-error-actions {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
