<script setup lang="ts">
import { Plus, X } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import type { AccessKeyFiltersDto, AccessProtocol } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'

import type {
  AccessKeyScopeDimension,
  AccessKeyScopeMode,
  AccessKeyScopeModes,
  GroupCatalogState,
} from './access-key-scope'

interface GroupOption {
  id: number
  label: string
  dangling: boolean
}

const props = defineProps<{
  modes: AccessKeyScopeModes
  filters: AccessKeyFiltersDto
  groupOptions: GroupOption[]
  groupCatalogState: GroupCatalogState
  protocolOptions: readonly AccessProtocol[]
  modelOptions: string[]
  modelInput: string
  baseGroupIds: number[]
  disabled: boolean
}>()
const emit = defineEmits<{
  setScopeMode: [dimension: AccessKeyScopeDimension, mode: AccessKeyScopeMode]
  toggleGroup: [groupID: number, checked: boolean]
  toggleProtocol: [protocol: AccessProtocol, checked: boolean]
  'update:modelInput': [value: string]
  addModel: []
  removeModel: [model: string]
}>()
const { t } = useI18n()

function catalogUnavailable(): boolean {
  return props.groupCatalogState === 'loading' || props.groupCatalogState === 'error'
}

function groupDisabled(groupID: number): boolean {
  return (
    props.disabled ||
    props.modes.groups !== 'restricted' ||
    catalogUnavailable() ||
    (props.groupCatalogState === 'stale' && !props.baseGroupIds.includes(groupID))
  )
}

function optionDisabled(dimension: 'protocols' | 'models'): boolean {
  return props.disabled || props.modes[dimension] !== 'restricted' || catalogUnavailable()
}

function requestScopeMode(dimension: AccessKeyScopeDimension, event: Event): void {
  const target = event.target as HTMLSelectElement
  emit('setScopeMode', dimension, target.value as AccessKeyScopeMode)
  target.value = props.modes[dimension]
}
</script>

<template>
  <fieldset>
    <legend>{{ t('accessKeys.drawer.groups') }}</legend>
    <label class="access-key-drawer__field">
      <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
      <select
        data-test="access-key-groups-mode"
        :value="modes.groups"
        :disabled="disabled || groupCatalogState !== 'ready'"
        @change="requestScopeMode('groups', $event)"
      >
        <option value="all">{{ t('accessKeys.drawer.scopeAll') }}</option>
        <option value="restricted">{{ t('accessKeys.drawer.scopeRestricted') }}</option>
      </select>
    </label>
    <label v-for="group in groupOptions" :key="group.id" class="access-key-drawer__check">
      <input
        type="checkbox"
        :checked="filters.groups.includes(group.id)"
        :disabled="groupDisabled(group.id)"
        @change="emit('toggleGroup', group.id, ($event.target as HTMLInputElement).checked)"
      />
      <span>{{ group.label }}</span>
    </label>
  </fieldset>

  <fieldset>
    <legend>{{ t('accessKeys.drawer.protocols') }}</legend>
    <label class="access-key-drawer__field">
      <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
      <select
        data-test="access-key-protocols-mode"
        :value="modes.protocols"
        :disabled="disabled || catalogUnavailable()"
        @change="requestScopeMode('protocols', $event)"
      >
        <option value="all">{{ t('accessKeys.drawer.scopeAll') }}</option>
        <option value="restricted">{{ t('accessKeys.drawer.scopeRestricted') }}</option>
      </select>
    </label>
    <label v-for="protocol in protocolOptions" :key="protocol" class="access-key-drawer__check">
      <input
        type="checkbox"
        :checked="filters.protocols.includes(protocol)"
        :disabled="optionDisabled('protocols')"
        @change="emit('toggleProtocol', protocol, ($event.target as HTMLInputElement).checked)"
      />
      <span class="access-key-drawer__check-content">
        <span>{{ t(`common.protocols.${protocol}`) }}</span>
        <small v-if="protocol === 'openai-response'">{{
          t('accessKeys.drawer.reservedProtocolHint')
        }}</small>
      </span>
    </label>
  </fieldset>

  <fieldset>
    <legend>{{ t('accessKeys.drawer.models') }}</legend>
    <label class="access-key-drawer__field">
      <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
      <select
        data-test="access-key-models-mode"
        :value="modes.models"
        :disabled="disabled || catalogUnavailable()"
        @change="requestScopeMode('models', $event)"
      >
        <option value="all">{{ t('accessKeys.drawer.scopeAll') }}</option>
        <option value="restricted">{{ t('accessKeys.drawer.scopeRestricted') }}</option>
      </select>
    </label>
    <p>{{ t('accessKeys.drawer.modelsDescription') }}</p>
    <div class="access-key-drawer__model-entry">
      <input
        :value="modelInput"
        data-test="access-key-model-input"
        type="text"
        list="access-key-model-options"
        autocomplete="off"
        :placeholder="t('accessKeys.drawer.modelPlaceholder')"
        :disabled="optionDisabled('models')"
        @input="emit('update:modelInput', ($event.target as HTMLInputElement).value)"
        @keydown.enter.prevent="emit('addModel')"
      />
      <datalist id="access-key-model-options">
        <option v-for="model in modelOptions" :key="model" :value="model" />
      </datalist>
      <AppButton
        data-test="access-key-model-add"
        variant="secondary"
        :disabled="optionDisabled('models') || !modelInput.trim()"
        @click="emit('addModel')"
      >
        <Plus :size="16" aria-hidden="true" />{{ t('accessKeys.drawer.addModel') }}
      </AppButton>
    </div>
    <div v-if="filters.models.length" class="access-key-drawer__models">
      <span v-for="model in filters.models" :key="model" class="access-key-drawer__model">
        <code>{{ model }}</code>
        <button
          type="button"
          :aria-label="t('accessKeys.drawer.removeModel', { model })"
          :disabled="catalogUnavailable() || disabled"
          @click="emit('removeModel', model)"
        >
          <X :size="15" aria-hidden="true" />
        </button>
      </span>
    </div>
  </fieldset>
</template>

<style scoped>
fieldset,
.access-key-drawer__field {
  display: grid;
  gap: var(--space-2);
}
fieldset {
  min-width: 0;
  margin: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.access-key-drawer__field > span,
legend {
  font-weight: 700;
}
fieldset p,
small {
  margin: 0;
  color: var(--color-text-muted);
}
input,
select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
.access-key-drawer__check {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
}
.access-key-drawer__check input {
  width: 20px;
  min-height: 20px;
}
.access-key-drawer__check-content {
  display: grid;
  gap: var(--space-1);
}
.access-key-drawer__model-entry {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.access-key-drawer__model-entry input {
  flex: 1 1 220px;
}
.access-key-drawer__models {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.access-key-drawer__model {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-1);
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding-left: var(--space-2);
}
.access-key-drawer__model button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
</style>
