<script setup lang="ts">
import { Plus } from '@lucide/vue'
import { computed } from 'vue'
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
const protocolMultiSelectOptions = computed<SearchableMultiSelectOption[]>(() =>
  props.protocolOptions.map((protocol) => ({
    value: protocol,
    label: t(`common.protocols.${protocol}`),
  })),
)

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

function updateProtocols(values: SearchableMultiSelectValue[]): void {
  const allowed = new Set<AccessProtocol>(props.protocolOptions)
  emit(
    'update:protocols',
    values.filter(
      (value): value is AccessProtocol =>
        typeof value === 'string' && allowed.has(value as AccessProtocol),
    ),
  )
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
        appearance="drawer"
        size="compact"
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
        :clear-search-label="t('accessKeys.collection.filters.clearSearch')"
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
        always-open
        auto-focus-search
        size="compact"
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
        appearance="drawer"
        size="compact"
        @update:model-value="requestScopeMode('protocols', $event as AccessKeyScopeMode)"
      />
    </div>
    <div class="scope-editor__body">
      <div v-if="modes.protocols === 'all'" class="permission-note">
        <i aria-hidden="true" />{{ t('accessKeys.drawer.allProtocolsAllowed') }}
      </div>
      <SearchableMultiSelect
        v-else
        id="access-key-protocols"
        :label="t('accessKeys.drawer.protocolSelectorLabel')"
        :search-label="t('accessKeys.drawer.protocols')"
        search-placeholder=""
        :clear-search-label="t('accessKeys.collection.filters.clearSearch')"
        :empty-label="t('accessKeys.drawer.noProtocolOptions')"
        :loading-label="t('accessKeys.drawer.groupOptionsLoading')"
        :selected-label="t('accessKeys.drawer.selectedCount', { count: '{count}' })"
        :add-label="t('accessKeys.drawer.addProtocol')"
        :clear-label="t('accessKeys.drawer.clearSelected')"
        :remove-label="(label) => t('accessKeys.drawer.removeSelection', { label })"
        :options="protocolMultiSelectOptions"
        :model-value="filters.protocols"
        :disabled="optionDisabled('protocols')"
        :searchable="false"
        always-open
        size="compact"
        @update:model-value="updateProtocols"
      />
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
        appearance="drawer"
        size="compact"
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
          :clear-search-label="t('accessKeys.collection.filters.clearSearch')"
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
          always-open
          auto-focus-search
          size="compact"
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
