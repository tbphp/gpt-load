<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyDto } from '@/api/control/types'

const props = defineProps<{
  name: string
  status: AccessKeyDto['status']
  rpmLimit: number
  disabled: boolean
}>()
const emit = defineEmits<{
  'update:name': [value: string]
  'update:status': [value: AccessKeyDto['status']]
  'update:rpmLimit': [value: number]
}>()
const { t } = useI18n()
const nameInput = ref<HTMLInputElement>()

function focusName(): void {
  nameInput.value?.focus()
}

function toggleStatus(): void {
  if (props.disabled) return
  emit('update:status', props.status === 'active' ? 'disabled' : 'active')
}

function updateRpm(event: Event): void {
  const value = (event.target as HTMLInputElement).value
  emit('update:rpmLimit', value === '' ? 0 : Number(value))
}

defineExpose({ focusName })
</script>

<template>
  <div class="access-key-form-stack">
    <label class="access-key-drawer__field" for="access-key-name">
      <span>
        {{ t('accessKeys.drawer.name') }}
        <b class="access-key-drawer__required" aria-hidden="true">*</b>
      </span>
      <input
        id="access-key-name"
        ref="nameInput"
        :value="name"
        type="text"
        autocomplete="off"
        :disabled="disabled"
        @input="emit('update:name', ($event.target as HTMLInputElement).value)"
      />
    </label>

    <div class="access-key-setting-row">
      <div class="access-key-setting-row__label">
        <strong>{{ t('accessKeys.drawer.enabled') }}</strong>
        <p>{{ t('accessKeys.drawer.enabledDescription') }}</p>
      </div>
      <button
        type="button"
        class="access-key-status-switch"
        :class="{ 'access-key-status-switch--on': status === 'active' }"
        role="switch"
        :aria-checked="status === 'active'"
        :aria-label="t('accessKeys.drawer.enabled')"
        :disabled="disabled"
        @click="toggleStatus"
      >
        <span aria-hidden="true" />
      </button>
    </div>

    <label class="access-key-drawer__field" for="access-key-rpm">
      <span>
        {{ t('accessKeys.drawer.rpm') }}
        <small class="access-key-drawer__optional">{{ t('accessKeys.drawer.optional') }}</small>
      </span>
      <input
        id="access-key-rpm"
        type="number"
        min="0"
        step="1"
        aria-describedby="access-key-rpm-description"
        :value="rpmLimit === 0 ? '' : rpmLimit"
        :placeholder="t('accessKeys.drawer.rpmPlaceholder')"
        :disabled="disabled"
        @input="updateRpm"
      />
      <small id="access-key-rpm-description">{{ t('accessKeys.drawer.rpmDescription') }}</small>
    </label>
  </div>
</template>

<style scoped>
.access-key-form-stack {
  display: grid;
  gap: 12px;
}
.access-key-drawer__field {
  display: grid;
  gap: 6px;
}
.access-key-drawer__field > span {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}
input {
  width: 100%;
  height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-meta);
}
small {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.access-key-drawer__required {
  color: var(--color-danger);
}
.access-key-drawer__optional {
  margin-left: var(--space-1);
  font-weight: 400;
}
.access-key-setting-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
}
.access-key-setting-row__label strong {
  display: block;
  font-size: var(--text-meta);
}
.access-key-setting-row__label p {
  margin: 3px 0 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.access-key-status-switch {
  position: relative;
  width: 40px;
  height: 22px;
  border: 1px solid var(--color-border-control);
  border-radius: 999px;
  background: var(--color-surface-sunken);
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}
.access-key-status-switch span {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--color-text-faint);
  transition: transform var(--duration-fast) var(--easing-standard);
}
.access-key-status-switch--on {
  border-color: var(--color-action);
  background: var(--color-action);
}
.access-key-status-switch--on span {
  transform: translateX(18px);
  background: var(--color-action-ink);
}
.access-key-status-switch:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
</style>
