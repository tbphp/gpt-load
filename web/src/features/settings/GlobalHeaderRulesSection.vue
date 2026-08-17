<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRulesDto } from '@/app/resources/groups'
import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import { createSettingsDraft, setSettingsOverride, type SettingsDraft } from './settings-patch'
import type { SettingsMergeConflict } from './settings-response'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  conflicts: SettingsMergeConflict[]
  resetKey?: number
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
  chooseMine: [key: RuntimeSettingKey]
  chooseLatest: [key: RuntimeSettingKey]
  'update:valid': [valid: boolean]
  'update:invalidEdits': [hasEdits: boolean]
}>()
const { t } = useI18n()
const key = 'header_rules' as const
const editorResetKey = ref(0)
const overridden = computed(() => props.draft.overrides.has(key))
const pendingRestore = computed(
  () => !overridden.value && props.base.settings.overrides.includes(key),
)
const rules = computed(() =>
  overridden.value || pendingRestore.value
    ? props.draft.values.header_rules
    : props.base.settings.values.header_rules,
)
const ruleCount = computed(() => Object.keys(rules.value.set).length + rules.value.remove.length)
const conflict = computed(() => props.conflicts.find((item) => item.key === key))

function cloneDraft(): SettingsDraft {
  return createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
}

function clearEditorState(): void {
  emit('update:valid', true)
  emit('update:invalidEdits', false)
}

async function resetEditor(): Promise<void> {
  clearEditorState()
  await nextTick()
  editorResetKey.value += 1
}

function toggleOverride(): void {
  clearEditorState()
  emit('change', {
    key,
    draft: setSettingsOverride(props.base.settings, props.draft, key, !overridden.value),
  })
  void resetEditor()
}

function updateRules(value: HeaderRulesDto): void {
  const draft = cloneDraft()
  draft.values.header_rules = value
  emit('change', { key, draft })
}

function conflictSummary(side: 'mine' | 'latest'): string {
  const identity = conflict.value?.[side]
  if (!identity?.is_override) return t('settings.headers.defaultSource')
  const value = identity.normalized_value as HeaderRulesDto
  return t('settings.headers.ruleCount', {
    count: Object.keys(value.set).length + value.remove.length,
  })
}

watch(
  () => props.resetKey,
  () => {
    void resetEditor()
  },
)
</script>

<template>
  <section id="settings-headers" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <div>
        <h2>{{ t('settings.headers.title') }}</h2>
        <p>{{ t('settings.headers.description') }}</p>
      </div>
      <div class="settings-headers__meta">
        <span>{{ t('settings.headers.ruleCount', { count: ruleCount }) }}</span>
        <StatusBadge
          size="compact"
          :tone="pendingRestore ? 'warning' : overridden ? 'info' : 'neutral'"
          :icon="pendingRestore ? 'alert' : overridden ? 'edit' : 'check'"
        >
          {{
            pendingRestore
              ? t('settings.headers.pendingRestoreSource')
              : overridden
                ? t('settings.headers.overrideSource')
                : t('settings.headers.defaultSource')
          }}
        </StatusBadge>
        <AppButton
          variant="secondary"
          :tone="overridden ? 'warning' : 'action'"
          size="compact"
          :disabled="disabled"
          @click="toggleOverride"
        >
          {{ overridden ? t('settings.headers.restoreDefault') : t('settings.headers.override') }}
        </AppButton>
      </div>
    </header>

    <div v-if="conflict" class="settings-headers__conflict" role="alert">
      <strong>{{ t('settings.headers.conflict') }}</strong>
      <span>{{ t('settings.conflict.mine') }}: {{ conflictSummary('mine') }}</span>
      <span>{{ t('settings.conflict.latest') }}: {{ conflictSummary('latest') }}</span>
      <div>
        <AppButton variant="secondary" size="compact" @click="emit('chooseMine', key)">
          {{ t('settings.conflict.useMine') }}
        </AppButton>
        <AppButton variant="ghost" size="compact" @click="emit('chooseLatest', key)">
          {{ t('settings.conflict.useLatest') }}
        </AppButton>
      </div>
    </div>

    <HeaderRulesEditor
      appearance="ledger"
      :model-value="rules"
      :disabled="disabled || !overridden"
      :reset-key="editorResetKey"
      :show-notice="false"
      :show-add="overridden"
      @update:model-value="updateRules"
      @update:valid="emit('update:valid', $event)"
      @update:invalid-edits="emit('update:invalidEdits', $event)"
    />
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.settings-headers__meta,
.settings-headers__conflict {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--space-4);
}

.settings-section__heading h2,
.settings-section__heading p {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--text-body);
  font-weight: 650;
}

.settings-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.settings-headers__meta {
  justify-items: end;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  text-align: end;
}

.settings-headers__meta :deep(.app-button) {
  margin-top: var(--space-1);
}

.settings-headers__conflict {
  gap: var(--space-1);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  font-size: var(--text-label-xs);
}

.settings-headers__conflict > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-1);
}

@media (max-width: 560px) {
  .settings-section__heading {
    grid-template-columns: 1fr;
  }

  .settings-headers__meta {
    justify-items: start;
    text-align: start;
  }
}
</style>
