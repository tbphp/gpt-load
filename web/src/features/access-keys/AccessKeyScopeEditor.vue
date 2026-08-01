<script setup lang="ts">
import { Plus } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyFiltersDto, AccessProtocol } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import SearchableMultiSelect, {
  type SearchableMultiSelectOption,
  type SearchableMultiSelectValue,
} from '@/components/ui/SearchableMultiSelect.vue'

import type {
  AccessKeyScopeDimension,
  AccessKeyScopeMode,
  AccessKeyScopeModes,
  GroupCatalogState,
} from './access-key-scope'

const props = defineProps<{
  modes: AccessKeyScopeModes
  filters: AccessKeyFiltersDto
  groupOptions: SearchableMultiSelectOption[]
  groupCatalogState: GroupCatalogState
  protocolOptions: readonly AccessProtocol[]
  supportedProtocols: readonly AccessProtocol[]
  modelOptions: string[]
  modelInput: string
  disabled: boolean
  modelMismatch: boolean
}>()
const emit = defineEmits<{
  setScopeMode: [dimension: AccessKeyScopeDimension, mode: AccessKeyScopeMode]
  'update:groups': [groupIDs: number[]]
  'update:protocols': [protocols: AccessProtocol[]]
  'update:models': [models: string[]]
  'update:modelInput': [value: string]
  addModel: []
}>()
const { t } = useI18n()

function catalogUnavailable(): boolean {
  return props.groupCatalogState === 'loading' || props.groupCatalogState === 'error'
}

function optionDisabled(dimension: 'protocols' | 'models'): boolean {
  return props.disabled || props.modes[dimension] !== 'restricted' || catalogUnavailable()
}

function requestScopeMode(dimension: AccessKeyScopeDimension, event: Event): void {
  const target = event.target as HTMLSelectElement
  emit('setScopeMode', dimension, target.value as AccessKeyScopeMode)
  target.value = props.modes[dimension]
}

function updateGroups(values: SearchableMultiSelectValue[]): void {
  emit(
    'update:groups',
    values.filter((value): value is number => typeof value === 'number'),
  )
}

function updateModels(values: SearchableMultiSelectValue[]): void {
  emit(
    'update:models',
    values.filter((value): value is string => typeof value === 'string'),
  )
}

function toggleProtocol(protocol: AccessProtocol, event: Event): void {
  const checked = (event.target as HTMLInputElement).checked
  const next = checked
    ? [...new Set([...props.filters.protocols, protocol])]
    : props.filters.protocols.filter((value) => value !== protocol)
  emit('update:protocols', next)
}

function protocolUnsupported(protocol: AccessProtocol): boolean {
  return (
    props.modes.groups === 'restricted' &&
    props.filters.groups.length > 0 &&
    !props.supportedProtocols.includes(protocol)
  )
}
</script>

<template>
  <fieldset>
    <legend>{{ t('accessKeys.drawer.groups') }}</legend>
    <label class="access-key-drawer__field">
      <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
      <select
        :value="modes.groups"
        :disabled="disabled || groupCatalogState !== 'ready'"
        @change="requestScopeMode('groups', $event)"
      >
        <option value="all">{{ t('accessKeys.drawer.scopeAll') }}</option>
        <option value="restricted">{{ t('accessKeys.drawer.scopeRestricted') }}</option>
      </select>
    </label>
    <SearchableMultiSelect
      id="access-key-groups"
      :label="t('accessKeys.drawer.groupSelectorLabel')"
      :search-label="t('accessKeys.drawer.searchGroups')"
      :search-placeholder="t('accessKeys.drawer.searchGroupsPlaceholder')"
      :empty-label="t('accessKeys.drawer.noGroupOptions')"
      :loading-label="t('accessKeys.drawer.groupOptionsLoading')"
      :selected-label="t('accessKeys.drawer.selectedCount', { count: '{count}' })"
      :clear-label="t('accessKeys.drawer.clearSelected')"
      :remove-label="(label) => t('accessKeys.drawer.removeSelection', { label })"
      :options="groupOptions"
      :model-value="filters.groups"
      :disabled="disabled || modes.groups !== 'restricted' || catalogUnavailable()"
      :loading="groupCatalogState === 'loading'"
      @update:model-value="updateGroups"
    />
  </fieldset>

  <fieldset>
    <legend>{{ t('accessKeys.drawer.protocols') }}</legend>
    <label class="access-key-drawer__field">
      <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
      <select
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
        @change="toggleProtocol(protocol, $event)"
      />
      <span class="access-key-drawer__check-content">
        <span>{{ t(`common.protocols.${protocol}`) }}</span>
        <small v-if="protocolUnsupported(protocol)" class="access-key-drawer__unsupported">{{
          t('accessKeys.drawer.protocolUnsupported')
        }}</small>
        <small v-else-if="protocol === 'openai-responses'">{{
          t('accessKeys.drawer.responsesAffinityHint')
        }}</small>
      </span>
    </label>
  </fieldset>

  <fieldset>
    <legend>{{ t('accessKeys.drawer.models') }}</legend>
    <label class="access-key-drawer__field">
      <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
      <select
        :value="modes.models"
        :disabled="disabled || catalogUnavailable()"
        @change="requestScopeMode('models', $event)"
      >
        <option value="all">{{ t('accessKeys.drawer.scopeAll') }}</option>
        <option value="restricted">{{ t('accessKeys.drawer.scopeRestricted') }}</option>
      </select>
    </label>
    <p>{{ t('accessKeys.drawer.modelsDescription') }}</p>
    <SearchableMultiSelect
      id="access-key-models"
      :label="t('accessKeys.drawer.modelSelectorLabel')"
      :search-label="t('accessKeys.drawer.searchModels')"
      :search-placeholder="t('accessKeys.drawer.searchModelsPlaceholder')"
      :empty-label="t('accessKeys.drawer.noModelOptions')"
      :loading-label="t('accessKeys.drawer.groupOptionsLoading')"
      :selected-label="t('accessKeys.drawer.selectedCount', { count: '{count}' })"
      :clear-label="t('accessKeys.drawer.clearSelected')"
      :remove-label="(label) => t('accessKeys.drawer.removeSelection', { label })"
      :options="modelOptions.map((model) => ({ value: model, label: model }))"
      :model-value="filters.models"
      :disabled="optionDisabled('models')"
      :loading="groupCatalogState === 'loading'"
      @update:model-value="updateModels"
    />
    <div class="access-key-drawer__model-entry">
      <input
        :value="modelInput"
        type="text"
        autocomplete="off"
        :placeholder="t('accessKeys.drawer.modelPlaceholder')"
        :disabled="optionDisabled('models')"
        @input="emit('update:modelInput', ($event.target as HTMLInputElement).value)"
        @keydown.enter.prevent="emit('addModel')"
      />
      <AppButton
        variant="secondary"
        :disabled="optionDisabled('models') || !modelInput.trim()"
        @click="emit('addModel')"
      >
        <Plus :size="16" aria-hidden="true" />{{ t('accessKeys.drawer.addModel') }}
      </AppButton>
    </div>
    <p v-if="modelMismatch" class="access-key-drawer__model-risk" role="status">
      {{ t('accessKeys.drawer.modelRouteRisk') }}
    </p>
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
  border: 1px solid var(--color-border-subtle);
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
  min-height: var(--touch-target);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
.access-key-drawer__check {
  display: flex;
  min-height: var(--touch-target);
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
.access-key-drawer__unsupported,
.access-key-drawer__model-risk {
  color: var(--color-warning);
}
.access-key-drawer__model-risk {
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  background: var(--color-warning-bg);
  padding: var(--space-2) var(--space-3);
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
</style>
