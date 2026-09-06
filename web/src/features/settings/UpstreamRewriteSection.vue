<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRulesDto } from '@/app/resources/groups'
import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'

import SettingBlock from '@/components/config/SettingBlock.vue'
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
const headerRulesKey = 'header_rules' as const
const editorResetKey = ref(0)
const headerRulesOverridden = computed(() => props.draft.overrides.has(headerRulesKey))
const headerRulesPendingRestore = computed(
  () => !headerRulesOverridden.value && props.base.settings.overrides.includes(headerRulesKey),
)
const headerRules = computed(() =>
  headerRulesOverridden.value || headerRulesPendingRestore.value
    ? props.draft.values.header_rules
    : props.base.settings.values.header_rules,
)
const headerRuleCount = computed(
  () => Object.keys(headerRules.value.set).length + headerRules.value.remove.length,
)

function cloneDraft(): SettingsDraft {
  return createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
}

function publish(key: RuntimeSettingKey, draft: SettingsDraft): void {
  emit('change', { key, draft })
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

function toggleHeaderRulesOverride(): void {
  clearEditorState()
  publish(
    headerRulesKey,
    setSettingsOverride(
      props.base.settings,
      props.draft,
      headerRulesKey,
      !headerRulesOverridden.value,
    ),
  )
  void resetEditor()
}

function updateHeaderRules(value: HeaderRulesDto): void {
  const draft = cloneDraft()
  draft.values.header_rules = value
  publish(headerRulesKey, draft)
}

function headerRulesSourceLabel(): string {
  if (headerRulesOverridden.value) return t('settings.headers.overrideSource')
  if (headerRulesPendingRestore.value) return t('settings.headers.pendingRestoreSource')
  return t('settings.headers.defaultSource')
}

watch(
  () => props.resetKey,
  () => {
    void resetEditor()
  },
)
</script>

<template>
  <section id="settings-upstream-rewrite" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.headers.title') }}</h2>
      <p>{{ t('settings.headers.description') }}</p>
    </header>

    <div class="upstream-rewrite__blocks">
      <SettingBlock
        :title="t('settings.headers.blockTitle')"
        :meta="t('settings.headers.ruleCount', { count: headerRuleCount })"
        :source-label="headerRulesSourceLabel()"
        :action-label="
          headerRulesOverridden
            ? t('settings.headers.restoreDefault')
            : t('settings.headers.override')
        "
        :overridden="headerRulesOverridden"
        :pending-restore="headerRulesPendingRestore"
        :disabled="disabled"
        @toggle="toggleHeaderRulesOverride"
      >
        <HeaderRulesEditor
          appearance="ledger"
          :model-value="headerRules"
          :disabled="disabled || !headerRulesOverridden"
          :reset-key="editorResetKey"
          :show-notice="false"
          :show-add="headerRulesOverridden"
          @update:model-value="updateHeaderRules"
          @update:valid="emit('update:valid', $event)"
          @update:invalid-edits="emit('update:invalidEdits', $event)"
        />
      </SettingBlock>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.upstream-rewrite__blocks {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading h2,
.settings-section__heading p {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--title-section);
  font-weight: 650;
}

.settings-section__heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.upstream-rewrite__blocks {
  gap: var(--space-5);
}
</style>
