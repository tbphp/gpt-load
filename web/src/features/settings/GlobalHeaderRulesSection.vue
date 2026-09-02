<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRulesDto } from '@/app/resources/groups'
import type { SettingsResource } from '@/app/resources/settings'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import { createSettingsDraft, setSettingsOverride, type SettingsDraft } from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  resetKey?: number
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
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
.settings-headers__meta {
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
