<script setup lang="ts">
import { Eye, EyeOff, Info, Plus, Trash2, X } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRules } from '@/features/import/model-draft'

interface RuleRow {
  key: number
  action: 'set' | 'remove'
  name: string
  value: string
  revealed: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue: HeaderRules
    disabled?: boolean
    appearance?: 'default' | 'ledger'
    removeLabel?: string
    removeHint?: string
  }>(),
  {
    disabled: false,
    appearance: 'default',
    removeLabel: undefined,
    removeHint: undefined,
  },
)
const emit = defineEmits<{
  'update:modelValue': [value: HeaderRules]
  'update:valid': [value: boolean]
}>()
const { t } = useI18n()
let nextKey = 1
const rows = ref<RuleRow[]>(createRows(normalizeRules(props.modelValue)))

function normalizeASCIIHeaderName(value: string): string {
  return value
    .trim()
    .replace(/[A-Z]/g, (character) => String.fromCharCode(character.charCodeAt(0) + 32))
}

function compareHeaderNames(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0
}

function normalizeRules(value: HeaderRules): HeaderRules {
  const set = Object.fromEntries(
    Object.entries(value.set)
      .map(([name, headerValue]) => [name.trim(), headerValue] as const)
      .filter(([name]) => name.length > 0)
      .sort(([left], [right]) => compareHeaderNames(left, right)),
  )
  const remove = [...new Set(value.remove.map((name) => name.trim()).filter(Boolean))].sort(
    compareHeaderNames,
  )
  return { set, remove }
}

function createRows(value: HeaderRules): RuleRow[] {
  return [
    ...Object.entries(value.set).map(([name, headerValue]) => ({
      key: nextKey++,
      action: 'set' as const,
      name,
      value: headerValue,
      revealed: false,
    })),
    ...value.remove.map((name) => ({
      key: nextKey++,
      action: 'remove' as const,
      name,
      value: '',
      revealed: false,
    })),
  ]
}

function rulesFromRows(): HeaderRules {
  const rules: HeaderRules = { set: {}, remove: [] }
  for (const row of rows.value) {
    const name = row.name.trim()
    if (!name) continue
    if (row.action === 'set') rules.set[name] = row.value
    else rules.remove.push(name)
  }
  return rules
}

function sameRules(left: HeaderRules, right: HeaderRules): boolean {
  return JSON.stringify(normalizeRules(left)) === JSON.stringify(normalizeRules(right))
}

const duplicateNames = computed(() => {
  const counts = new Map<string, number>()
  for (const row of rows.value) {
    const normalized = normalizeASCIIHeaderName(row.name)
    if (normalized) counts.set(normalized, (counts.get(normalized) ?? 0) + 1)
  }
  return new Set([...counts].filter(([, count]) => count > 1).map(([name]) => name))
})

watch(
  () => duplicateNames.value.size === 0,
  (valid) => emit('update:valid', valid),
  { immediate: true },
)

watch(
  () => props.modelValue,
  (value) => {
    const external = normalizeRules(value)
    if (sameRules(rulesFromRows(), external)) return
    rows.value = createRows(external)
  },
  { deep: true },
)

function publish(): void {
  emit('update:modelValue', rulesFromRows())
}

function addRow(): void {
  rows.value.push({ key: nextKey++, action: 'set', name: '', value: '', revealed: false })
  publish()
}

function removeRow(key: number): void {
  rows.value = rows.value.filter((row) => row.key !== key)
  publish()
}

function setAction(row: RuleRow, event: Event): void {
  setActionValue(row, (event.target as HTMLSelectElement).value as RuleRow['action'])
}

function setActionValue(row: RuleRow, action: RuleRow['action']): void {
  row.action = action
  if (row.action === 'remove') row.value = ''
  publish()
}

function setName(row: RuleRow, event: Event): void {
  row.name = (event.target as HTMLInputElement).value
  publish()
}

function setValue(row: RuleRow, event: Event): void {
  row.value = (event.target as HTMLInputElement).value
  publish()
}
</script>

<template>
  <section
    class="header-rules"
    :class="`header-rules--${appearance}`"
    :aria-label="t('import.headerRules.title')"
  >
    <div v-if="appearance === 'default'" class="header-rules__heading">
      <div>
        <h3>{{ t('import.headerRules.title') }}</h3>
        <p>{{ t('import.headerRules.description') }}</p>
        <p>
          {{ t('import.headerRules.storageNotice', { template: '${API_KEY}' }) }}
        </p>
      </div>
      <button class="header-rules__add" type="button" :disabled="props.disabled" @click="addRow">
        <Plus :size="16" aria-hidden="true" />{{ t('import.headerRules.add') }}
      </button>
    </div>
    <div v-else class="header-rules__notice">
      <Info :size="16" aria-hidden="true" />
      <span>
        <slot name="notice">{{
          t('import.headerRules.storageNotice', { template: '${API_KEY}' })
        }}</slot>
      </span>
    </div>

    <div v-if="rows.length" class="header-rules__rows">
      <div v-for="row in rows" :key="row.key" class="header-rule">
        <template v-if="appearance === 'ledger'">
          <label class="sr-only" :for="`header-name-${row.key}`">{{
            t('import.headerRules.name')
          }}</label>
          <input
            :id="`header-name-${row.key}`"
            class="header-rule__name"
            :value="row.name"
            :placeholder="t('import.headerRules.name')"
            autocomplete="off"
            spellcheck="false"
            :disabled="props.disabled"
            @input="setName(row, $event)"
          />
          <div class="header-rule__mode" role="group" :aria-label="t('import.headerRules.action')">
            <button
              type="button"
              :aria-pressed="row.action === 'set'"
              :disabled="props.disabled"
              @click="setActionValue(row, 'set')"
            >
              {{ t('import.headerRules.set') }}
            </button>
            <button
              type="button"
              data-mode="remove"
              :aria-pressed="row.action === 'remove'"
              :disabled="props.disabled"
              @click="setActionValue(row, 'remove')"
            >
              {{ removeLabel ?? t('import.headerRules.remove') }}
            </button>
          </div>
          <template v-if="row.action === 'set'">
            <label class="sr-only" :for="`header-value-${row.key}`">{{
              t('import.headerRules.value')
            }}</label>
            <input
              :id="`header-value-${row.key}`"
              class="header-rule__value"
              type="text"
              :value="row.value"
              :placeholder="t('import.headerRules.value')"
              autocomplete="off"
              spellcheck="false"
              :disabled="props.disabled"
              @input="setValue(row, $event)"
            />
          </template>
          <span v-else class="header-rule__remove-hint">{{
            removeHint ?? t('import.headerRules.removeHint')
          }}</span>
          <button
            class="header-rule__icon"
            type="button"
            :aria-label="t('import.headerRules.delete')"
            :disabled="props.disabled"
            @click="removeRow(row.key)"
          >
            <X :size="16" aria-hidden="true" />
          </button>
        </template>
        <template v-else>
          <label class="sr-only" :for="`header-action-${row.key}`">{{
            t('import.headerRules.action')
          }}</label>
          <select
            :id="`header-action-${row.key}`"
            :value="row.action"
            :disabled="props.disabled"
            @change="setAction(row, $event)"
          >
            <option value="set">{{ t('import.headerRules.set') }}</option>
            <option value="remove">{{ t('import.headerRules.remove') }}</option>
          </select>
          <label class="sr-only" :for="`header-name-${row.key}`">{{
            t('import.headerRules.name')
          }}</label>
          <input
            :id="`header-name-${row.key}`"
            :value="row.name"
            :placeholder="t('import.headerRules.name')"
            autocomplete="off"
            spellcheck="false"
            :disabled="props.disabled"
            @input="setName(row, $event)"
          />
          <div v-if="row.action === 'set'" class="header-rule__secret">
            <label class="sr-only" :for="`header-value-${row.key}`">{{
              t('import.headerRules.value')
            }}</label>
            <input
              :id="`header-value-${row.key}`"
              :type="row.revealed ? 'text' : 'password'"
              :value="row.value"
              :placeholder="t('import.headerRules.value')"
              autocomplete="off"
              spellcheck="false"
              :disabled="props.disabled"
              @input="setValue(row, $event)"
            />
            <button
              class="header-rule__icon"
              type="button"
              :aria-label="row.revealed ? t('common.conceal') : t('common.reveal')"
              :disabled="props.disabled"
              @click="row.revealed = !row.revealed"
            >
              <EyeOff v-if="row.revealed" :size="16" aria-hidden="true" />
              <Eye v-else :size="16" aria-hidden="true" />
            </button>
          </div>
          <span v-else class="header-rule__remove-hint">{{
            t('import.headerRules.removeHint')
          }}</span>
          <button
            class="header-rule__icon"
            type="button"
            :aria-label="t('import.headerRules.delete')"
            :disabled="props.disabled"
            @click="removeRow(row.key)"
          >
            <Trash2 :size="16" aria-hidden="true" />
          </button>
        </template>
      </div>
    </div>
    <p v-if="duplicateNames.size" class="header-rules__error" role="alert">
      {{ t('import.headerRules.duplicate') }}
    </p>
    <button
      v-if="appearance === 'ledger'"
      class="header-rules__add"
      type="button"
      :disabled="props.disabled"
      @click="addRow"
    >
      <Plus :size="16" aria-hidden="true" />{{ t('import.headerRules.add') }}
    </button>
  </section>
</template>

<style scoped>
.header-rules {
  display: grid;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-4);
}
.header-rules__heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
h3,
p {
  margin: 0;
}
h3 {
  font-size: 0.875rem;
}
.header-rules__heading p,
.header-rule__remove-hint {
  color: var(--color-text-muted);
  font-size: 0.75rem;
}
.header-rules__notice {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  border: 1px solid color-mix(in srgb, var(--color-warning) 34%, var(--color-border-subtle));
  border-radius: var(--radius-control);
  background: var(--color-warning-bg);
  color: var(--color-warning);
  padding: 10px 12px;
  font-size: var(--text-sm);
}
.header-rules__notice :deep(code) {
  background: transparent;
  color: inherit;
  padding: 0;
  font-family: var(--font-mono);
  font-size: inherit;
}
.header-rules__add,
.header-rule__icon {
  display: inline-flex;
  min-height: 44px;
  min-width: 44px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-action);
  cursor: pointer;
}
.header-rules__add {
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  font-weight: 650;
}
.header-rules__rows {
  display: grid;
  gap: var(--space-2);
}
.header-rule {
  display: grid;
  grid-template-columns: 110px minmax(140px, 1fr) minmax(180px, 1.3fr) 44px;
  gap: var(--space-2);
  align-items: center;
}
.header-rule select,
.header-rule input {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
}
.header-rule__secret {
  position: relative;
}
.header-rule__secret input {
  padding-right: 48px;
  font-family: ui-monospace, monospace;
}
.header-rule__secret .header-rule__icon {
  position: absolute;
  top: 0;
  right: 0;
  border-color: transparent;
}
.header-rules__error {
  color: var(--color-danger);
  font-size: 0.8125rem;
}
.header-rules--ledger {
  gap: 11px;
  border-top: 0;
  padding-top: 0;
}
.header-rules--ledger .header-rules__rows {
  gap: var(--space-2);
}
.header-rules--ledger .header-rule {
  grid-template-columns: minmax(120px, 0.8fr) auto minmax(0, 1.2fr) 32px;
}
.header-rules--ledger .header-rule__mode {
  grid-column: 2;
  grid-row: 1;
}
.header-rules--ledger .header-rule__name {
  grid-column: 1;
  grid-row: 1;
}
.header-rules--ledger .header-rule__value,
.header-rules--ledger .header-rule__remove-hint {
  grid-column: 3;
  grid-row: 1;
}
.header-rules--ledger .header-rule > .header-rule__icon {
  grid-column: 4;
  grid-row: 1;
}
.header-rules--ledger .header-rule__name,
.header-rules--ledger .header-rule__value,
.header-rules--ledger .header-rule__remove-hint {
  min-height: 36px;
  background: var(--color-surface);
  padding: 6px 9px;
  font-family: var(--font-mono);
  font-size: 11px;
}
.header-rules--ledger .header-rule__mode {
  display: inline-flex;
  overflow: hidden;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
}
.header-rules--ledger .header-rule__mode button {
  min-width: 52px;
  min-height: 34px;
  border: 0;
  border-left: 1px solid var(--color-border-control);
  background: var(--color-surface);
  color: var(--color-text-faint);
  padding: 5px 8px;
  font: inherit;
  font-size: var(--text-label-xs);
  white-space: nowrap;
  cursor: pointer;
}
.header-rules--ledger .header-rule__mode button:first-child {
  border-left: 0;
}
.header-rules--ledger .header-rule__mode button[aria-pressed='true'] {
  background: var(--color-text);
  color: var(--color-surface);
  font-weight: 560;
}
.header-rules--ledger .header-rule__mode button[data-mode='remove'][aria-pressed='true'] {
  background: var(--color-danger-bg);
  color: var(--color-danger);
}
.header-rules--ledger .header-rule__remove-hint {
  display: flex;
  align-items: center;
  border: 1px dashed color-mix(in srgb, var(--color-warning) 46%, var(--color-border-subtle));
  border-radius: var(--radius-control);
  background: var(--color-warning-bg);
  color: var(--color-warning);
  line-height: 1.4;
}
.header-rules--ledger .header-rule > .header-rule__icon {
  min-width: 32px;
  min-height: 32px;
}
.header-rules--ledger .header-rules__add {
  width: fit-content;
  min-width: 0;
  min-height: var(--control-md);
  justify-content: flex-start;
  border: 0;
  margin-top: 2px;
  padding: 5px 1px;
}
@media (max-width: 800px) {
  .header-rules--ledger .header-rule__name,
  .header-rules--ledger .header-rule__value,
  .header-rules--ledger .header-rule__remove-hint {
    min-height: var(--touch-target);
    font-size: 16px;
  }
  .header-rules--ledger .header-rule__mode button,
  .header-rules--ledger .header-rule > .header-rule__icon,
  .header-rules--ledger .header-rules__add {
    min-height: var(--touch-target);
  }
  .header-rules--ledger .header-rule > .header-rule__icon {
    min-width: var(--touch-target);
  }
}
@media (max-width: 520px) {
  .header-rule {
    grid-template-columns: 1fr 44px;
  }
  .header-rule > :not(.header-rule__icon) {
    grid-column: 1;
  }
  .header-rule > .header-rule__icon {
    grid-column: 2;
    grid-row: 1;
  }
  .header-rules--ledger .header-rule {
    grid-template-columns: minmax(0, 1fr) 32px;
  }
  .header-rules--ledger .header-rule__name,
  .header-rules--ledger .header-rule__mode,
  .header-rules--ledger .header-rule__value,
  .header-rules--ledger .header-rule__remove-hint {
    grid-column: 1;
    grid-row: auto;
  }
  .header-rules--ledger .header-rule > .header-rule__icon {
    grid-column: 2;
    grid-row: 1;
  }
}
</style>
