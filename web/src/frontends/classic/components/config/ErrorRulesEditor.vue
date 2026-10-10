<script setup lang="ts">
import { ArrowDown, ArrowUp, ChevronDown, Copy, Plus, Trash2, X } from '@lucide/vue'
import { computed, nextTick, ref, useId, watch } from 'vue'
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
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import FormField from '@/components/ui/FormField.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = defineProps<{
  modelValue: ErrorRule[]
  disabled?: boolean
  readonly?: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: ErrorRule[]]
  'update:valid': [value: boolean]
}>()
const { t, n } = useI18n()
const instanceId = useId()
let nextKey = 0
const rows = ref(props.modelValue.map((rule) => errorRuleDraft(rule, nextKey++)))
const elements = new Map<number, HTMLElement>()
const announcement = ref('')
const inactive = computed(() => props.disabled || props.readonly)
const values = computed(() => rows.value.map(errorRuleValue))
const errors = computed(() => new Map(rows.value.map((row) => [row.key, errorRuleErrors(row)])))
const valid = computed(() => validErrorRules(values.value))
let synchronized = JSON.stringify(values.value)
let emitted = JSON.stringify(props.modelValue)

watch(valid, (value) => emit('update:valid', value), { immediate: true })
watch(
  values,
  (value) => {
    const encoded = JSON.stringify(value)
    if (encoded === synchronized) return
    synchronized = encoded
    emitted = encoded
    emit('update:modelValue', value)
  },
  { deep: true },
)
watch(
  () => props.modelValue,
  (value) => {
    const encoded = JSON.stringify(value)
    if (encoded === emitted) return
    emitted = encoded
    const next = value.map((rule) => errorRuleDraft(rule, nextKey++))
    synchronized = JSON.stringify(next.map(errorRuleValue))
    rows.value = next
  },
  { deep: true },
)

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
function addStatus(row: ErrorRuleDraft, event: KeyboardEvent): void {
  if (event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  event.stopPropagation()
  if (inactive.value) return
  const value = row.statusInput.trim()
  if (!value) {
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
function addKeyword(row: ErrorRuleDraft, event: KeyboardEvent): void {
  if (event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  event.stopPropagation()
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
  return !validErrorRules([values.value[index]!])
}
async function focusFirstInvalid(): Promise<void> {
  const row = rows.value.find((_, index) => rowInvalid(index))
  if (row) await focusRule(row)
}
defineExpose({ focusFirstInvalid })
</script>

<template>
  <div class="error-rules">
    <div v-if="rows.length || !readonly" class="error-rules__bar">
      <span class="error-rules__order">
        <ArrowDown :size="13" aria-hidden="true" />
        {{ t('errorRules.order') }}
      </span>
      <AppButton
        v-if="!readonly"
        size="compact"
        :disabled="disabled || rows.length >= 100"
        @click="add"
      >
        <Plus :size="15" aria-hidden="true" />
        {{ t('errorRules.add') }}
      </AppButton>
    </div>
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <p v-if="!rows.length" class="error-rules__empty">{{ t('errorRules.empty') }}</p>
    <div v-else class="error-rules__list">
      <article
        v-for="(row, index) in rows"
        :key="row.key"
        :ref="(element) => rowRef(row.key, element)"
        class="error-rule"
        :class="{ 'error-rule--invalid': rowInvalid(index) && !row.open }"
      >
        <header class="error-rule__head">
          <button
            type="button"
            class="error-rule__toggle"
            :aria-expanded="row.open"
            :aria-controls="instanceId + '-' + row.key"
            @click="row.open = !row.open"
          >
            <ChevronDown class="error-rule__chevron" :size="15" aria-hidden="true" />
            <span class="error-rule__index">{{ n(index + 1) }}</span>
            <OverflowTooltip class="error-rule__match-summary" :content="matchSummary(row)">
              <span>{{ matchSummary(row) }}</span>
            </OverflowTooltip>
          </button>
          <div v-if="!readonly" class="error-rule__tools">
            <div class="error-rule__move">
              <AppTooltip :content="t('errorRules.up')">
                <IconButton
                  variant="ghost"
                  size="xs"
                  :label="t('errorRules.up')"
                  :disabled="disabled || index === 0"
                  @click="move(index, -1)"
                  ><ArrowUp :size="15" aria-hidden="true"
                /></IconButton>
              </AppTooltip>
              <AppTooltip :content="t('errorRules.down')">
                <IconButton
                  variant="ghost"
                  size="xs"
                  :label="t('errorRules.down')"
                  :disabled="disabled || index === rows.length - 1"
                  @click="move(index, 1)"
                  ><ArrowDown :size="15" aria-hidden="true"
                /></IconButton>
              </AppTooltip>
            </div>
            <AppTooltip :content="t('errorRules.copy')">
              <IconButton
                variant="ghost"
                size="xs"
                :label="t('errorRules.copy')"
                :disabled="disabled || rows.length >= 100"
                @click="copy(index)"
                ><Copy :size="15" aria-hidden="true"
              /></IconButton>
            </AppTooltip>
            <AppTooltip :content="t('errorRules.remove')">
              <IconButton
                variant="ghost"
                tone="danger"
                size="xs"
                :label="t('errorRules.remove')"
                :disabled="disabled"
                @click="remove(index)"
                ><Trash2 :size="15" aria-hidden="true"
              /></IconButton>
            </AppTooltip>
          </div>
        </header>
        <div v-if="!row.open" :id="instanceId + '-' + row.key" class="error-rule__summary">
          <StatusBadge v-if="rowInvalid(index)" tone="warning" size="compact">
            {{ t('errorRules.incomplete') }}
          </StatusBadge>
          <span class="error-rule__chip">{{ t('errorRules.retries.' + row.retry) }}</span>
          <span class="error-rule__chip">
            <OverflowTooltip :content="effectSummary(row)"
              ><span>{{ effectSummary(row) }}</span></OverflowTooltip
            >
          </span>
        </div>
        <div v-else :id="instanceId + '-' + row.key" class="error-rule__body">
          <div class="error-rule__match">
            <FormField
              :id="instanceId + '-statuses-' + row.key"
              :label="t('errorRules.statuses')"
              :error="errors.get(row.key)?.statuses ? t('errorRules.errors.statuses') : undefined"
              size="compact"
            >
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  :id="instanceId + '-statuses-' + row.key"
                  v-model="row.statusInput"
                  :label="t('errorRules.statuses')"
                  :placeholder="t('errorRules.statusesPlaceholder')"
                  inputmode="numeric"
                  :invalid="invalid"
                  :described-by="describedBy"
                  :disabled="inactive"
                  autocomplete="off"
                  size="compact"
                  @update:model-value="row.statusInvalid = false"
                  @keydown.enter="addStatus(row, $event)"
                />
                <div v-if="selectedStatuses(row).length" class="error-rule__tags">
                  <span v-for="code in selectedStatuses(row)" :key="code" class="error-rule__tag">
                    <span>{{ code }}</span>
                    <AppTooltip
                      v-if="!readonly"
                      :content="t('errorRules.removeStatus', { value: code })"
                    >
                      <IconButton
                        class="error-rule__tag-remove"
                        :label="t('errorRules.removeStatus', { value: code })"
                        variant="ghost"
                        tone="action"
                        size="xxs"
                        :disabled="disabled"
                        @click="removeStatus(row, code)"
                        ><X :size="12" aria-hidden="true"
                      /></IconButton>
                    </AppTooltip>
                  </span>
                </div>
              </template>
            </FormField>
            <FormField
              :id="instanceId + '-keywords-' + row.key"
              :label="t('errorRules.keywords')"
              size="compact"
            >
              <AppTextInput
                :id="instanceId + '-keywords-' + row.key"
                v-model="row.keywordInput"
                :label="t('errorRules.keywords')"
                :placeholder="t('errorRules.keywordsPlaceholder')"
                :disabled="inactive"
                :spellcheck="false"
                size="compact"
                @keydown.enter="addKeyword(row, $event)"
              />
              <div v-if="keywords(row).length" class="error-rule__tags">
                <span v-for="keyword in keywords(row)" :key="keyword" class="error-rule__tag">
                  <OverflowTooltip class="error-rule__tag-text" :content="keyword">
                    <span>{{ keyword }}</span>
                  </OverflowTooltip>
                  <AppTooltip
                    v-if="!readonly"
                    :content="t('errorRules.removeStatus', { value: keyword })"
                  >
                    <IconButton
                      class="error-rule__tag-remove"
                      :label="t('errorRules.removeStatus', { value: keyword })"
                      variant="ghost"
                      tone="action"
                      size="xxs"
                      :disabled="disabled"
                      @click="removeKeyword(row, keyword)"
                      ><X :size="12" aria-hidden="true"
                    /></IconButton>
                  </AppTooltip>
                </span>
              </div>
            </FormField>
          </div>
          <div class="error-rule__actions">
            <FormField
              :id="instanceId + '-retry-' + row.key"
              :label="t('errorRules.retry')"
              size="compact"
            >
              <div class="error-rule__retry-control">
                <AppSwitch
                  :id="instanceId + '-retry-' + row.key"
                  :model-value="row.retry === 'next_candidate'"
                  :label="t('errorRules.retry')"
                  :disabled="inactive"
                  @update:model-value="row.retry = $event ? 'next_candidate' : 'none'"
                />
              </div>
            </FormField>
            <div
              class="error-rule__effects"
              :class="{ 'has-cooldown': hasErrorRuleCooldown(row.effect) }"
            >
              <FormField
                :id="instanceId + '-effect-' + row.key"
                :label="t('errorRules.effect')"
                size="compact"
              >
                <AppSelect
                  :id="instanceId + '-effect-' + row.key"
                  v-model="row.effect"
                  :label="t('errorRules.effect')"
                  :options="effects"
                  :disabled="inactive"
                  size="compact"
                />
              </FormField>
              <FormField
                v-if="hasErrorRuleCooldown(row.effect)"
                :id="instanceId + '-cooldown-' + row.key"
                :label="t('errorRules.cooldown')"
                :error="errors.get(row.key)?.cooldown ? t('errorRules.errors.cooldown') : undefined"
                size="compact"
              >
                <template #default="{ invalid, describedBy }">
                  <AppTextInput
                    :id="instanceId + '-cooldown-' + row.key"
                    v-model="row.cooldown"
                    :label="t('errorRules.cooldown')"
                    :placeholder="t('errorRules.cooldownPlaceholder')"
                    inputmode="numeric"
                    :invalid="invalid"
                    :described-by="describedBy"
                    :disabled="inactive"
                    size="compact"
                  />
                </template>
              </FormField>
            </div>
          </div>
        </div>
      </article>
    </div>
    <InlineFeedback
      v-if="!valid && rows.every((row) => !Object.keys(errorRuleErrors(row)).length)"
      tone="warning"
      >{{ t('errorRules.errors.invalid') }}</InlineFeedback
    >
  </div>
</template>

<style scoped>
.error-rules {
  container: error-rules / inline-size;
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}
.error-rules__bar,
.error-rules__order,
.error-rule__head,
.error-rule__tools,
.error-rule__move {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.error-rules__bar {
  flex-wrap: wrap;
  justify-content: space-between;
}
.error-rules__order {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: var(--line-normal);
}
.error-rules__empty {
  margin: 0;
  padding: var(--space-3) var(--space-4);
  border: 1px dashed var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-faint);
  font-size: var(--text-meta);
}
.error-rules__list {
  overflow: hidden;
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.error-rule + .error-rule {
  border-top: 1px solid var(--color-border-subtle);
}
.error-rule--invalid {
  box-shadow: inset 2px 0 var(--color-danger);
}
.error-rule__head {
  min-height: 42px;
  gap: var(--space-1);
}
.error-rule__head:hover {
  background: var(--color-interactive-hover);
}
.error-rule__toggle {
  display: grid;
  flex: 1;
  min-width: 0;
  grid-template-columns: auto 18px minmax(0, 1fr);
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  border: 0;
  background: transparent;
  color: var(--color-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.error-rule__chevron {
  color: var(--color-text-faint);
  transition: transform var(--duration-fast) var(--easing-standard);
}
.error-rule__toggle[aria-expanded='true'] .error-rule__chevron {
  transform: rotate(180deg);
}
.error-rule__index {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  text-align: center;
}
.error-rule__match-summary {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.error-rule__tools,
.error-rule__move {
  flex: none;
  gap: 0;
}
.error-rule__tools {
  padding-right: var(--space-2);
}
.error-rule__move {
  padding-right: var(--space-1);
  margin-right: var(--space-1);
  border-right: 1px solid var(--color-border-subtle);
}
.error-rule__summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: 0 var(--space-3) var(--space-2) 34px;
}
.error-rule__chip {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.error-rule__body {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  padding: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  background: color-mix(in srgb, var(--color-surface-sunken) 42%, var(--color-surface));
}
.error-rule__match,
.error-rule__actions,
.error-rule__effects {
  display: grid;
  align-items: start;
  min-width: 0;
  gap: var(--space-3);
}
.error-rule__match {
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.4fr);
}
.error-rule__actions {
  grid-template-columns: 5rem minmax(0, 1fr);
}
.error-rule__effects {
  grid-template-columns: minmax(0, 1fr);
}
.error-rule__effects.has-cooldown {
  grid-template-columns: minmax(0, 1fr) 14rem;
}
.error-rule__retry-control {
  display: flex;
  align-items: center;
  min-height: var(--control-compact);
}
.error-rule__tags {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
  gap: 6px;
}
.error-rule__tag {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  min-width: 0;
  min-height: var(--control-xxs);
  gap: var(--space-1);
  padding: 0 var(--space-1) 0 var(--space-2);
  border: 1px solid var(--color-action);
  border-radius: var(--radius-tag);
  background: transparent;
  color: var(--color-action);
  font-size: var(--text-label-xs);
}
.error-rule__tag-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.error-rule__tag .error-rule__tag-remove {
  width: calc(var(--control-xxs) - 2px);
  height: calc(var(--control-xxs) - 2px);
}
@media (max-width: 860px) {
  .error-rule__tag .error-rule__tag-remove {
    width: var(--touch-target);
    height: var(--touch-target);
  }
}
@container error-rules (max-width: 640px) {
  .error-rule__effects.has-cooldown {
    grid-template-columns: minmax(0, 1fr);
  }
}
@container error-rules (max-width: 480px) {
  .error-rule__head {
    flex-wrap: wrap;
  }
  .error-rule__toggle {
    flex-basis: 100%;
  }
  .error-rule__tools {
    margin-left: auto;
  }
  .error-rule__match,
  .error-rule__actions {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 860px) {
  .error-rule__retry-control {
    min-height: var(--touch-target);
  }
}
</style>
