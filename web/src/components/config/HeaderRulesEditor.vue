<script setup lang="ts">
import { Eye, EyeOff, Plus, Trash2 } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRules } from '@/features/import/model-draft'

interface RuleRow {
  key: number
  action: 'set' | 'remove'
  name: string
  value: string
  revealed: boolean
}

const props = defineProps<{ modelValue: HeaderRules }>()
const emit = defineEmits<{ 'update:modelValue': [value: HeaderRules] }>()
const { t } = useI18n()
const touchTargetStyle = { minWidth: '44px', minHeight: '44px' }
let nextKey = 1
const rows = ref<RuleRow[]>([
  ...Object.entries(props.modelValue.set).map(([name, value]) => ({
    key: nextKey++,
    action: 'set' as const,
    name,
    value,
    revealed: false,
  })),
  ...props.modelValue.remove.map((name) => ({
    key: nextKey++,
    action: 'remove' as const,
    name,
    value: '',
    revealed: false,
  })),
])

function normalizeASCIIHeaderName(value: string): string {
  return value
    .trim()
    .replace(/[A-Z]/g, (character) => String.fromCharCode(character.charCodeAt(0) + 32))
}

const duplicateNames = computed(() => {
  const counts = new Map<string, number>()
  for (const row of rows.value) {
    const normalized = normalizeASCIIHeaderName(row.name)
    if (normalized) counts.set(normalized, (counts.get(normalized) ?? 0) + 1)
  }
  return new Set([...counts].filter(([, count]) => count > 1).map(([name]) => name))
})

function publish(): void {
  const rules: HeaderRules = { set: {}, remove: [] }
  for (const row of rows.value) {
    const name = row.name.trim()
    if (!name) continue
    if (row.action === 'set') rules.set[name] = row.value
    else rules.remove.push(name)
  }
  emit('update:modelValue', rules)
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
  row.action = (event.target as HTMLSelectElement).value as RuleRow['action']
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
  <section class="header-rules" :aria-label="t('import.headerRules.title')">
    <div class="header-rules__heading">
      <div>
        <h3>{{ t('import.headerRules.title') }}</h3>
        <p>{{ t('import.headerRules.description') }}</p>
      </div>
      <button data-test="add-header-rule" class="header-rules__add" type="button" @click="addRow">
        <Plus :size="16" aria-hidden="true" />{{ t('import.headerRules.add') }}
      </button>
    </div>

    <div v-if="rows.length" class="header-rules__rows">
      <div v-for="row in rows" :key="row.key" class="header-rule">
        <label class="sr-only" :for="`header-action-${row.key}`">{{
          t('import.headerRules.action')
        }}</label>
        <select
          :id="`header-action-${row.key}`"
          data-test="header-action"
          :value="row.action"
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
          data-test="header-name"
          :value="row.name"
          :placeholder="t('import.headerRules.name')"
          autocomplete="off"
          spellcheck="false"
          @input="setName(row, $event)"
        />
        <div v-if="row.action === 'set'" class="header-rule__secret">
          <label class="sr-only" :for="`header-value-${row.key}`">{{
            t('import.headerRules.value')
          }}</label>
          <input
            :id="`header-value-${row.key}`"
            data-test="header-value"
            :type="row.revealed ? 'text' : 'password'"
            :value="row.value"
            :placeholder="t('import.headerRules.value')"
            autocomplete="off"
            spellcheck="false"
            @input="setValue(row, $event)"
          />
          <button
            data-test="toggle-header-value"
            class="header-rule__icon"
            type="button"
            :style="touchTargetStyle"
            :aria-label="row.revealed ? t('common.conceal') : t('common.reveal')"
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
          data-test="delete-header-rule"
          class="header-rule__icon"
          type="button"
          :style="touchTargetStyle"
          :aria-label="t('import.headerRules.delete')"
          @click="removeRow(row.key)"
        >
          <Trash2 :size="16" aria-hidden="true" />
        </button>
      </div>
    </div>
    <p v-if="duplicateNames.size" class="header-rules__error" role="alert">
      {{ t('import.headerRules.duplicate') }}
    </p>
  </section>
</template>

<style scoped>
.header-rules {
  display: grid;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border);
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
.header-rules__add,
.header-rule__icon {
  display: inline-flex;
  min-height: 44px;
  min-width: 44px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-primary);
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
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
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
@media (max-width: 760px) {
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
}
</style>
