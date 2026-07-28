<script setup lang="ts">
import { ChevronDown, SlidersHorizontal } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRulesDto } from '@/app/resources/groups'
import type { RuntimeSettingKey, TimeoutSettingKey } from '@/app/resources/settings'
import type { SettingsResource } from '@/app/resources/settings'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import SettingOverrideField from './SettingOverrideField.vue'
import {
  createSettingsDraft,
  isValidTimeout,
  setSettingsOverride,
  type SettingsDraft,
} from './settings-patch'
import type { SettingsMergeConflict } from './settings-response'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  conflicts: SettingsMergeConflict[]
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
  chooseMine: [key: RuntimeSettingKey]
  chooseLatest: [key: RuntimeSettingKey]
  'update:headerRulesValid': [valid: boolean]
}>()
const { t } = useI18n()
const disclosureRequested = ref(
  props.draft.overrides.has('header_rules') ||
    props.conflicts.some((conflict) => conflict.key === 'header_rules'),
)
const timeoutKeys: TimeoutSettingKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
const relevantConflicts = computed(() =>
  props.conflicts.filter(
    (conflict) =>
      conflict.key === 'header_rules' ||
      timeoutKeys.some((timeoutKey) => timeoutKey === conflict.key),
  ),
)
const headerOpen = computed(
  () =>
    disclosureRequested.value ||
    props.draft.overrides.has('header_rules') ||
    relevantConflicts.value.some((conflict) => conflict.key === 'header_rules'),
)

function cloneCurrentDraft(): SettingsDraft {
  return createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
  })
}

function publish(key: RuntimeSettingKey, draft: SettingsDraft): void {
  emit('change', { key, draft })
}

function hasOverride(key: TimeoutSettingKey | 'header_rules'): boolean {
  return props.draft.overrides.has(key)
}

function setOverride(key: TimeoutSettingKey, enabled: boolean): void {
  publish(key, setSettingsOverride(props.base.settings, props.draft, key, enabled))
}

function setTimeoutValue(key: TimeoutSettingKey, value: number): void {
  const draft = cloneCurrentDraft()
  draft.values[key] = value
  publish(key, draft)
}

function timeoutError(key: TimeoutSettingKey): string | undefined {
  return hasOverride(key) && !isValidTimeout(props.draft.values[key])
    ? t('settings.request.timeoutError')
    : undefined
}

function setHeaderOverride(enabled: boolean): void {
  if (enabled) disclosureRequested.value = true
  publish(
    'header_rules',
    setSettingsOverride(props.base.settings, props.draft, 'header_rules', enabled),
  )
}

function setHeaderRules(value: HeaderRulesDto): void {
  const draft = cloneCurrentDraft()
  draft.values.header_rules = value
  publish('header_rules', draft)
}

function toggleDisclosure(): void {
  disclosureRequested.value = !headerOpen.value
}

function conflictValue(conflict: SettingsMergeConflict, side: 'mine' | 'latest'): string {
  const identity = conflict[side]
  if (!identity.is_override) return t('settings.default')
  if (conflict.key === 'header_rules') {
    const rules = identity.normalized_value as HeaderRulesDto
    return t('settings.conflict.headerRulesSummary', {
      set: Object.keys(rules.set).length,
      remove: rules.remove.length,
    })
  }
  return String(identity.normalized_value)
}

function conflictLabel(key: RuntimeSettingKey): string {
  return key === 'header_rules' ? t('settings.request.headerRules') : t(`settings.request.${key}`)
}
</script>

<template>
  <SurfaceCard
    id="settings-request-forwarding"
    class="settings-card request-forwarding"
    tabindex="-1"
  >
    <header class="settings-card__heading">
      <div class="settings-card__title">
        <span class="settings-card__icon"><SlidersHorizontal :size="18" aria-hidden="true" /></span>
        <div>
          <h2>{{ t('settings.request.title') }}</h2>
          <p>{{ t('settings.request.description') }}</p>
        </div>
      </div>
    </header>

    <ul
      v-if="relevantConflicts.length > 0"
      class="settings-conflicts"
      data-test="settings-conflicts"
    >
      <li v-for="conflict in relevantConflicts" :key="conflict.key">
        <strong>{{ conflictLabel(conflict.key) }}</strong>
        <span>{{ t('settings.conflict.mine') }}: {{ conflictValue(conflict, 'mine') }}</span>
        <span>{{ t('settings.conflict.latest') }}: {{ conflictValue(conflict, 'latest') }}</span>
        <div>
          <AppButton
            :data-test="`settings-conflict-mine-${conflict.key}`"
            variant="secondary"
            @click="emit('chooseMine', conflict.key)"
          >
            {{ t('settings.conflict.useMine') }}
          </AppButton>
          <AppButton
            :data-test="`settings-conflict-latest-${conflict.key}`"
            variant="ghost"
            @click="emit('chooseLatest', conflict.key)"
          >
            {{ t('settings.conflict.useLatest') }}
          </AppButton>
        </div>
      </li>
    </ul>

    <div class="request-forwarding__fields">
      <SettingOverrideField
        v-for="key in timeoutKeys"
        :key="key"
        :setting-key="key"
        :label="t(`settings.request.${key}`)"
        :description="t('settings.request.seconds')"
        :effective-value="base.settings.values[key]"
        :owned="hasOverride(key)"
        :model-value="draft.values[key]"
        :error="timeoutError(key)"
        :min="1"
        :disabled="disabled"
        @update:owned="setOverride(key, $event)"
        @update:model-value="setTimeoutValue(key, $event)"
      />
    </div>

    <section class="request-forwarding__advanced">
      <button
        id="settings-header-rules"
        data-test="settings-header-disclosure"
        class="request-forwarding__disclosure"
        type="button"
        :aria-expanded="headerOpen"
        aria-controls="settings-header-rules-editor"
        @click="toggleDisclosure"
      >
        <span>
          <strong>{{ t('settings.request.headerRules') }}</strong>
          <small>{{
            t('settings.request.headerSummary', {
              set: Object.keys(base.settings.values.header_rules.set).length,
              remove: base.settings.values.header_rules.remove.length,
            })
          }}</small>
        </span>
        <StatusBadge :tone="hasOverride('header_rules') ? 'warning' : 'neutral'">
          {{ hasOverride('header_rules') ? t('settings.override') : t('settings.default') }}
        </StatusBadge>
        <ChevronDown :size="18" aria-hidden="true" :class="{ rotated: headerOpen }" />
      </button>

      <div
        v-if="headerOpen"
        id="settings-header-rules-editor"
        class="request-forwarding__advanced-body"
      >
        <label class="request-forwarding__header-toggle">
          <input
            data-test="override-header_rules"
            type="checkbox"
            :checked="hasOverride('header_rules')"
            :disabled="disabled"
            @change="setHeaderOverride(($event.target as HTMLInputElement).checked)"
          />
          {{ t('settings.useOverride') }}
        </label>
        <InlineFeedback tone="warning">{{ t('settings.request.headerWarning') }}</InlineFeedback>
        <div v-if="hasOverride('header_rules')" data-test="header-rules-editor">
          <HeaderRulesEditor
            :model-value="draft.values.header_rules"
            :disabled="disabled"
            @update:model-value="setHeaderRules"
            @update:valid="emit('update:headerRulesValid', $event)"
          />
        </div>
      </div>
    </section>
  </SurfaceCard>
</template>

<style scoped>
.settings-card,
.settings-card__title,
.settings-card__heading,
.request-forwarding__fields,
.request-forwarding__advanced,
.request-forwarding__advanced-body {
  display: grid;
}
.settings-card,
.settings-card__heading {
  gap: var(--space-4);
}
.settings-card__title {
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-3);
}
.settings-card__heading h2,
.settings-card__heading p {
  margin: 0;
}
.settings-card__heading h2 {
  font-size: 1rem;
}
.settings-card__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.settings-card__icon {
  display: inline-flex;
  width: 36px;
  height: 36px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-control);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.request-forwarding__fields,
.request-forwarding__advanced-body {
  gap: var(--space-4);
}
.request-forwarding__advanced {
  gap: var(--space-3);
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-4);
}
.settings-conflicts {
  display: grid;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.settings-conflicts li {
  display: grid;
  gap: var(--space-2);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.settings-conflicts li > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.request-forwarding__disclosure {
  display: grid;
  min-height: 44px;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
  border: 0;
  background: transparent;
  color: var(--color-text);
  padding: 0;
  text-align: left;
  font: inherit;
  cursor: pointer;
}
.request-forwarding__disclosure span:first-child {
  display: grid;
  gap: var(--space-1);
}
.request-forwarding__disclosure small {
  color: var(--color-text-muted);
}
.request-forwarding__disclosure svg {
  transition: transform var(--duration-fast) ease;
}
.request-forwarding__disclosure svg.rotated {
  transform: rotate(180deg);
}
.request-forwarding__header-toggle {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}
.request-forwarding__header-toggle input {
  width: 18px;
  height: 18px;
}
@media (max-width: 640px) {
  .request-forwarding__disclosure {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .request-forwarding__disclosure :deep(.status-badge) {
    grid-column: 1;
    width: fit-content;
  }
  .request-forwarding__disclosure > svg {
    grid-column: 2;
    grid-row: 1;
  }
}
@media (prefers-reduced-motion: reduce) {
  .request-forwarding__disclosure svg {
    transition: none;
  }
}
</style>
