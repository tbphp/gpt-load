<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRulesDto } from '@/app/resources/groups'
import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import SettingRow from './SettingRow.vue'
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

function hasOverride(key: RuntimeSettingKey): boolean {
  return props.draft.overrides.has(key)
}

function isPendingRestore(key: RuntimeSettingKey): boolean {
  return !hasOverride(key) && props.base.settings.overrides.includes(key)
}

function toggleOverride(key: RuntimeSettingKey): void {
  publish(key, setSettingsOverride(props.base.settings, props.draft, key, !hasOverride(key)))
}

function sourceLabel(key: RuntimeSettingKey): string {
  if (hasOverride(key)) return t('settings.runtime.overrideSource')
  if (isPendingRestore(key)) return t('settings.runtime.pendingRestoreSource')
  return t('settings.runtime.defaultSource')
}

function actionLabel(key: RuntimeSettingKey): string {
  return hasOverride(key) ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
}

function setInjectUsage(value: boolean): void {
  const draft = cloneDraft()
  draft.values.inject_usage_options = value
  publish('inject_usage_options', draft)
}

const injectUsageValue = computed(() => {
  if (isPendingRestore('inject_usage_options')) return t('settings.runtime.resetPending')
  return props.base.settings.values.inject_usage_options
    ? t('settings.runtime.enabled')
    : t('settings.runtime.disabled')
})
</script>

<template>
  <section id="settings-upstream-rewrite" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.headers.title') }}</h2>
      <p>{{ t('settings.headers.description') }}</p>
    </header>

    <div class="upstream-rewrite__blocks">
      <article class="upstream-rewrite__block">
        <header class="upstream-rewrite__block-heading">
          <span>{{ t('settings.headers.ruleCount', { count: headerRuleCount }) }}</span>
          <StatusBadge
            size="compact"
            :tone="
              headerRulesPendingRestore ? 'warning' : headerRulesOverridden ? 'info' : 'neutral'
            "
            :icon="headerRulesPendingRestore ? 'alert' : headerRulesOverridden ? 'edit' : 'check'"
          >
            {{ headerRulesSourceLabel() }}
          </StatusBadge>
          <AppButton
            variant="secondary"
            :tone="headerRulesOverridden ? 'warning' : 'action'"
            size="compact"
            :disabled="disabled"
            @click="toggleHeaderRulesOverride"
          >
            {{
              headerRulesOverridden
                ? t('settings.headers.restoreDefault')
                : t('settings.headers.override')
            }}
          </AppButton>
        </header>

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
      </article>

      <SettingRow
        :label="t('settings.runtime.inject_usage_options')"
        :value="injectUsageValue"
        :help="t('settings.runtime.injectUsageHelp')"
        :source-label="sourceLabel('inject_usage_options')"
        :action-label="actionLabel('inject_usage_options')"
        :overridden="hasOverride('inject_usage_options')"
        :pending-restore="isPendingRestore('inject_usage_options')"
        :divided="false"
        :disabled="disabled"
        @toggle="toggleOverride('inject_usage_options')"
      >
        <template #control>
          <AppSwitch
            :model-value="draft.values.inject_usage_options"
            :disabled="disabled"
            :label="t('settings.runtime.inject_usage_options')"
            @update:model-value="setInjectUsage"
          />
        </template>
      </SettingRow>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.upstream-rewrite__blocks,
.upstream-rewrite__block {
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
  gap: var(--space-4);
}

.upstream-rewrite__block {
  gap: var(--space-3);
  border-bottom: 1px dashed var(--color-border-subtle);
  padding-bottom: var(--space-4);
}

.upstream-rewrite__block-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.upstream-rewrite__block-heading span:first-child {
  margin-right: auto;
}

@media (max-width: 560px) {
  .upstream-rewrite__block-heading {
    justify-content: flex-start;
  }

  .upstream-rewrite__block-heading span:first-child {
    margin-right: 0;
    width: 100%;
  }
}
</style>
