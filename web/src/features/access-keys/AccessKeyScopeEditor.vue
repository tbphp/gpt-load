<script setup lang="ts">
import { Plus } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyFiltersDto, AccessProtocol } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'
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
  modelOptions: SearchableMultiSelectOption[]
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

function requestScopeMode(dimension: AccessKeyScopeDimension, mode: AccessKeyScopeMode): void {
  emit('setScopeMode', dimension, mode)
}

function modeDisabled(dimension: AccessKeyScopeDimension): boolean {
  if (props.disabled) return true
  return dimension === 'groups' ? props.groupCatalogState !== 'ready' : catalogUnavailable()
}

function modeOptions(dimension: AccessKeyScopeDimension): SegmentedControlOption[] {
  const disabled = modeDisabled(dimension)
  return [
    { value: 'all', label: t('accessKeys.drawer.scopeAll'), disabled },
    { value: 'restricted', label: t('accessKeys.drawer.scopeRestricted'), disabled },
  ]
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
</script>

<template>
  <fieldset class="scope-editor">
    <legend class="sr-only">{{ t('accessKeys.drawer.groups') }}</legend>
    <div class="scope-editor__head">
      <div>
        <strong>{{ t('accessKeys.drawer.groups') }}</strong>
        <small>{{ t('accessKeys.drawer.groupsDescription') }}</small>
      </div>
      <SegmentedControl
        :model-value="modes.groups"
        :label="t('accessKeys.drawer.groups')"
        :options="modeOptions('groups')"
        size="touch"
        @update:model-value="requestScopeMode('groups', $event as AccessKeyScopeMode)"
      />
    </div>
    <div class="scope-editor__body">
      <div v-if="modes.groups === 'all'" class="permission-note">
        <i aria-hidden="true" />{{ t('accessKeys.drawer.allGroupsAllowed') }}
      </div>
      <SearchableMultiSelect
        v-else
        id="access-key-groups"
        :label="t('accessKeys.drawer.groupSelectorLabel')"
        :search-label="t('accessKeys.drawer.searchGroups')"
        :search-placeholder="t('accessKeys.drawer.searchGroupsPlaceholder')"
        :empty-label="t('accessKeys.drawer.noGroupOptions')"
        :loading-label="t('accessKeys.drawer.groupOptionsLoading')"
        :selected-label="t('accessKeys.drawer.selectedCount', { count: '{count}' })"
        :add-label="t('accessKeys.drawer.addGroup')"
        :clear-label="t('accessKeys.drawer.clearSelected')"
        :remove-label="(label) => t('accessKeys.drawer.removeSelection', { label })"
        :options="groupOptions"
        :model-value="filters.groups"
        :disabled="disabled || catalogUnavailable()"
        :loading="groupCatalogState === 'loading'"
        @update:model-value="updateGroups"
      />
    </div>
  </fieldset>

  <fieldset class="scope-editor">
    <legend class="sr-only">{{ t('accessKeys.drawer.protocols') }}</legend>
    <div class="scope-editor__head">
      <div>
        <strong>{{ t('accessKeys.drawer.protocols') }}</strong>
        <small>{{ t('accessKeys.drawer.protocolsDescription') }}</small>
      </div>
      <SegmentedControl
        :model-value="modes.protocols"
        :label="t('accessKeys.drawer.protocols')"
        :options="modeOptions('protocols')"
        size="touch"
        @update:model-value="requestScopeMode('protocols', $event as AccessKeyScopeMode)"
      />
    </div>
    <div class="scope-editor__body">
      <div v-if="modes.protocols === 'all'" class="permission-note">
        <i aria-hidden="true" />{{ t('accessKeys.drawer.allProtocolsAllowed') }}
      </div>
      <div v-else class="protocol-options">
        <label v-for="protocol in protocolOptions" :key="protocol" class="access-key-drawer__check">
          <input
            type="checkbox"
            :checked="filters.protocols.includes(protocol)"
            :disabled="optionDisabled('protocols')"
            @change="toggleProtocol(protocol, $event)"
          />
          <span class="access-key-drawer__check-content">
            <span>{{ t(`common.protocols.${protocol}`) }}</span>
          </span>
        </label>
      </div>
    </div>
  </fieldset>

  <fieldset class="scope-editor">
    <legend class="sr-only">{{ t('accessKeys.drawer.models') }}</legend>
    <div class="scope-editor__head">
      <div>
        <strong>{{ t('accessKeys.drawer.models') }}</strong>
        <small>{{ t('accessKeys.drawer.modelsScopeDescription') }}</small>
      </div>
      <SegmentedControl
        :model-value="modes.models"
        :label="t('accessKeys.drawer.models')"
        :options="modeOptions('models')"
        size="touch"
        @update:model-value="requestScopeMode('models', $event as AccessKeyScopeMode)"
      />
    </div>
    <div class="scope-editor__body">
      <div v-if="modes.models === 'all'" class="permission-note">
        <i aria-hidden="true" />{{ t('accessKeys.drawer.allModelsAllowed') }}
      </div>
      <template v-else>
        <SearchableMultiSelect
          id="access-key-models"
          :label="t('accessKeys.drawer.modelSelectorLabel')"
          :search-label="t('accessKeys.drawer.searchModels')"
          :search-placeholder="t('accessKeys.drawer.searchModelsPlaceholder')"
          :empty-label="t('accessKeys.drawer.noModelOptions')"
          :loading-label="t('accessKeys.drawer.groupOptionsLoading')"
          :selected-label="t('accessKeys.drawer.selectedCount', { count: '{count}' })"
          :add-label="t('accessKeys.drawer.addModel')"
          :clear-label="t('accessKeys.drawer.clearSelected')"
          :remove-label="(label) => t('accessKeys.drawer.removeSelection', { label })"
          :options="modelOptions"
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
            size="compact"
            :disabled="optionDisabled('models') || !modelInput.trim()"
            @click="emit('addModel')"
          >
            <Plus :size="14" aria-hidden="true" />{{ t('accessKeys.drawer.addModel') }}
          </AppButton>
        </div>
        <p v-if="modelMismatch" class="access-key-drawer__model-risk" role="status">
          {{ t('accessKeys.drawer.modelRouteRisk') }}
        </p>
      </template>
    </div>
  </fieldset>
</template>

<style scoped>
.scope-editor {
  min-width: 0;
  margin: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  padding: 0;
}
.scope-editor + .scope-editor {
  margin-top: 10px;
}
.scope-editor__head {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 8px 10px;
}
.scope-editor__head strong {
  display: block;
  font-size: var(--text-sm);
}
.scope-editor__head small {
  display: block;
  margin-top: 1px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.scope-editor__body {
  padding: 10px;
}
.permission-note {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.permission-note i {
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: 50%;
  background: var(--color-action);
}
.protocol-options {
  display: grid;
  gap: 6px;
}
.access-key-drawer__check {
  display: flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-2);
  border-radius: var(--radius-tag);
  padding: 4px 7px;
}
.access-key-drawer__check:hover {
  background: var(--color-surface-sunken);
}
.access-key-drawer__check input {
  width: 15px;
  height: 15px;
  flex: none;
}
.access-key-drawer__check-content {
  display: grid;
  gap: 2px;
  font-size: var(--text-sm);
}
.access-key-drawer__check-content small {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.access-key-drawer__model-risk {
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  background: var(--color-warning-bg);
  color: var(--color-warning);
  margin: 8px 0 0;
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-label-xs);
}
.access-key-drawer__model-entry {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: 8px;
}
.access-key-drawer__model-entry input {
  min-width: 0;
  height: var(--control-compact);
  flex: 1 1 220px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: 0 10px;
  font-size: var(--text-sm);
}
</style>
