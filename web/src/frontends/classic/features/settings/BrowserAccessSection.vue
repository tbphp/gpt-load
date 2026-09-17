<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRulesDto } from '@/app/resources/groups'
import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import SettingBlock from '@/components/config/SettingBlock.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'

import {
  createSettingsDraft,
  isValidCORSConfig,
  setSettingsOverride,
  type SettingsDraft,
} from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

type CORSListKey = 'allowed_origins' | 'allowed_methods' | 'allowed_headers' | 'exposed_headers'
type ToggleableKey = 'header_rules' | 'cors' | 'response_header_rules'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  resetKey: number
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
  'update:valid': [value: boolean]
  'update:headerRulesValid': [value: boolean]
  'update:corsValid': [value: boolean]
  'update:responseRulesValid': [value: boolean]
  'update:headerRulesInvalidEdits': [value: boolean]
  'update:responseRulesInvalidEdits': [value: boolean]
}>()
const { t } = useI18n()

const headerRulesRawValid = ref(true)
const headerRulesInvalidEdits = ref(false)
const headerRulesEditorResetKey = ref(0)
const responseRulesValid = ref(true)
const responseRulesInvalidEdits = ref(false)
const responseEditorResetKey = ref(0)

const headerRulesOverridden = computed(() => props.draft.overrides.has('header_rules'))
const corsOverridden = computed(() => props.draft.overrides.has('cors'))
const responseRulesOverridden = computed(() => props.draft.overrides.has('response_header_rules'))
const headerRulesPendingRestore = computed(
  () => !headerRulesOverridden.value && props.base.settings.overrides.includes('header_rules'),
)
const corsPendingRestore = computed(
  () => !corsOverridden.value && props.base.settings.overrides.includes('cors'),
)
const responseRulesPendingRestore = computed(
  () =>
    !responseRulesOverridden.value &&
    props.base.settings.overrides.includes('response_header_rules'),
)
const headerRules = computed(() =>
  headerRulesOverridden.value || headerRulesPendingRestore.value
    ? props.draft.values.header_rules
    : props.base.settings.values.header_rules,
)
const headerRuleCount = computed(
  () => Object.keys(headerRules.value.set).length + headerRules.value.remove.length,
)
const cors = computed(() =>
  corsOverridden.value ? props.draft.values.cors : props.base.settings.values.cors,
)
const responseRules = computed(() =>
  responseRulesOverridden.value || responseRulesPendingRestore.value
    ? props.draft.values.response_header_rules
    : props.base.settings.values.response_header_rules,
)
const responseRuleCount = computed(
  () => Object.keys(responseRules.value.set).length + responseRules.value.remove.length,
)
const effectiveHeaderRulesValid = computed(
  () => !headerRulesOverridden.value || headerRulesRawValid.value,
)
const corsValid = computed(
  () => !corsOverridden.value || isValidCORSConfig(props.draft.values.cors),
)
const effectiveResponseRulesValid = computed(
  () => !responseRulesOverridden.value || responseRulesValid.value,
)
const valid = computed(
  () => effectiveHeaderRulesValid.value && corsValid.value && effectiveResponseRulesValid.value,
)

watch(valid, (value) => emit('update:valid', value), { immediate: true })
watch(headerRulesRawValid, (value) => emit('update:headerRulesValid', value), { immediate: true })
watch(corsValid, (value) => emit('update:corsValid', value), { immediate: true })
watch(effectiveResponseRulesValid, (value) => emit('update:responseRulesValid', value), {
  immediate: true,
})
watch(headerRulesInvalidEdits, (value) => emit('update:headerRulesInvalidEdits', value), {
  immediate: true,
})
watch(responseRulesInvalidEdits, (value) => emit('update:responseRulesInvalidEdits', value), {
  immediate: true,
})
watch(
  () => props.resetKey,
  () => {
    headerRulesRawValid.value = true
    headerRulesInvalidEdits.value = false
    headerRulesEditorResetKey.value += 1
    responseRulesValid.value = true
    responseRulesInvalidEdits.value = false
    responseEditorResetKey.value += 1
  },
)
watch(headerRulesOverridden, (overridden) => {
  if (!overridden) {
    headerRulesRawValid.value = true
    headerRulesInvalidEdits.value = false
  }
})
watch(responseRulesOverridden, (overridden) => {
  if (!overridden) {
    responseRulesValid.value = true
    responseRulesInvalidEdits.value = false
  }
})

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

async function toggleOverride(key: ToggleableKey): Promise<void> {
  publish(
    key,
    setSettingsOverride(props.base.settings, props.draft, key, !props.draft.overrides.has(key)),
  )
  if (key === 'header_rules') {
    headerRulesRawValid.value = true
    headerRulesInvalidEdits.value = false
    await nextTick()
    headerRulesEditorResetKey.value += 1
  }
  if (key === 'response_header_rules') {
    responseRulesValid.value = true
    responseRulesInvalidEdits.value = false
    await nextTick()
    responseEditorResetKey.value += 1
  }
}

function updateHeaderRules(value: HeaderRulesDto): void {
  const draft = cloneDraft()
  draft.values.header_rules = value
  publish('header_rules', draft)
}

function setCORSEnabled(value: boolean): void {
  const draft = cloneDraft()
  draft.values.cors.enabled = value
  publish('cors', draft)
}

function setCORSList(key: CORSListKey, value: string): void {
  const draft = cloneDraft()
  draft.values.cors[key] = splitList(value)
  publish('cors', draft)
}

function setAllowCredentials(value: boolean): void {
  const draft = cloneDraft()
  draft.values.cors.allow_credentials = value
  publish('cors', draft)
}

function setMaxAge(value: string): void {
  const draft = cloneDraft()
  draft.values.cors.max_age = value.trim() === '' ? Number.NaN : Number(value)
  publish('cors', draft)
}

function updateResponseRules(value: HeaderRulesDto): void {
  const draft = cloneDraft()
  draft.values.response_header_rules = value
  publish('response_header_rules', draft)
}

function splitList(value: string): string[] {
  if (value.trim() === '') return []
  return value.split(',').map((entry) => entry.trim())
}

function joined(values: string[]): string {
  return values.join(', ')
}

function unique(values: string[], caseInsensitive = false): boolean {
  const normalized = values.map((value) => (caseInsensitive ? value.toLowerCase() : value))
  return new Set(normalized).size === normalized.length
}

function validHTTPToken(value: string): boolean {
  return value.length > 0 && /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/u.test(value)
}

function validOrigin(value: string): boolean {
  if (value === '*' || value === 'null') return true
  return (
    value === value.trim() &&
    !value.includes('@') &&
    /^[A-Za-z][A-Za-z0-9+.-]*:\/\/[^/?#\s,]+$/u.test(value)
  )
}

function validHeaderList(values: string[], required: boolean): boolean {
  if (required && values.length === 0) return false
  return (
    unique(values, true) &&
    values.every((value) => value === '*' || validHTTPToken(value)) &&
    (!values.includes('*') || values.length === 1)
  )
}

const originsError = computed(() => {
  const values = cors.value.allowed_origins
  if (cors.value.enabled && values.length === 0) return t('settings.browserAccess.errors.origins')
  if (
    !unique(values) ||
    values.some((value) => !validOrigin(value)) ||
    (values.includes('*') && values.length > 1) ||
    (values.includes('*') && cors.value.allow_credentials)
  ) {
    return t('settings.browserAccess.errors.origins')
  }
  return undefined
})
const methodsError = computed(() => {
  const values = cors.value.allowed_methods
  return (cors.value.enabled && values.length === 0) ||
    !unique(values, true) ||
    !values.every((method) => method !== '*' && validHTTPToken(method))
    ? t('settings.browserAccess.errors.methods')
    : undefined
})
const allowedHeadersError = computed(() =>
  !validHeaderList(cors.value.allowed_headers, cors.value.enabled)
    ? t('settings.browserAccess.errors.headers')
    : undefined,
)
const exposedHeadersError = computed(() =>
  !validHeaderList(cors.value.exposed_headers, false) ||
  (cors.value.allow_credentials && cors.value.exposed_headers.includes('*'))
    ? t('settings.browserAccess.errors.headers')
    : undefined,
)
const maxAgeError = computed(() =>
  Number.isSafeInteger(cors.value.max_age) && cors.value.max_age >= 0
    ? undefined
    : t('settings.browserAccess.errors.maxAge'),
)

function sourceLabel(overridden: boolean, pendingRestore: boolean): string {
  if (overridden) return t('settings.runtime.overrideSource')
  if (pendingRestore) return t('settings.runtime.pendingRestoreSource')
  return t('settings.runtime.defaultSource')
}
</script>

<template>
  <section id="settings-browser-access" class="settings-section" tabindex="-1">
    <header class="settings-section__heading">
      <h2>{{ t('settings.browserAccess.title') }}</h2>
      <p>{{ t('settings.browserAccess.description') }}</p>
    </header>

    <div class="browser-access__blocks">
      <SettingBlock
        :title="t('settings.browserAccess.cors.title')"
        :help="t('settings.browserAccess.cors.description')"
        :source-label="sourceLabel(corsOverridden, corsPendingRestore)"
        :action-label="
          corsOverridden ? t('settings.runtime.restoreDefault') : t('settings.runtime.override')
        "
        :overridden="corsOverridden"
        :pending-restore="corsPendingRestore"
        :disabled="disabled"
        @toggle="toggleOverride('cors')"
      >
        <div v-if="corsOverridden" class="browser-access__cors-form">
          <div class="browser-access__switch-field browser-access__field--wide">
            <div>
              <strong>{{ t('settings.browserAccess.cors.enabled') }}</strong>
              <small>{{ t('settings.browserAccess.cors.enabledHelp') }}</small>
            </div>
            <AppSwitch
              :model-value="cors.enabled"
              :disabled="disabled"
              :label="t('settings.browserAccess.cors.enabled')"
              @update:model-value="setCORSEnabled"
            />
          </div>

          <div class="browser-access__field browser-access__field--wide">
            <span>{{ t('settings.browserAccess.cors.allowedOrigins') }}</span>
            <CompactFieldError id="settings-value-cors-origins" :error="originsError">
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  id="settings-value-cors-origins"
                  :model-value="joined(cors.allowed_origins)"
                  :label="t('settings.browserAccess.cors.allowedOrigins')"
                  :placeholder="t('settings.browserAccess.cors.allowedOriginsPlaceholder')"
                  appearance="surface"
                  size="compact"
                  monospace
                  :disabled="disabled"
                  :invalid="invalid"
                  :described-by="describedBy"
                  @update:model-value="setCORSList('allowed_origins', $event)"
                />
              </template>
            </CompactFieldError>
          </div>

          <div class="browser-access__field">
            <span>{{ t('settings.browserAccess.cors.allowedMethods') }}</span>
            <CompactFieldError id="settings-value-cors-methods" :error="methodsError">
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  id="settings-value-cors-methods"
                  :model-value="joined(cors.allowed_methods)"
                  :label="t('settings.browserAccess.cors.allowedMethods')"
                  appearance="surface"
                  size="compact"
                  monospace
                  :disabled="disabled"
                  :invalid="invalid"
                  :described-by="describedBy"
                  @update:model-value="setCORSList('allowed_methods', $event)"
                />
              </template>
            </CompactFieldError>
          </div>

          <div class="browser-access__field">
            <span>{{ t('settings.browserAccess.cors.allowedHeaders') }}</span>
            <CompactFieldError id="settings-value-cors-headers" :error="allowedHeadersError">
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  id="settings-value-cors-headers"
                  :model-value="joined(cors.allowed_headers)"
                  :label="t('settings.browserAccess.cors.allowedHeaders')"
                  appearance="surface"
                  size="compact"
                  monospace
                  :disabled="disabled"
                  :invalid="invalid"
                  :described-by="describedBy"
                  @update:model-value="setCORSList('allowed_headers', $event)"
                />
              </template>
            </CompactFieldError>
          </div>

          <div class="browser-access__field">
            <span>{{ t('settings.browserAccess.cors.exposedHeaders') }}</span>
            <CompactFieldError id="settings-value-cors-exposed" :error="exposedHeadersError">
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  id="settings-value-cors-exposed"
                  :model-value="joined(cors.exposed_headers)"
                  :label="t('settings.browserAccess.cors.exposedHeaders')"
                  appearance="surface"
                  size="compact"
                  monospace
                  :disabled="disabled"
                  :invalid="invalid"
                  :described-by="describedBy"
                  @update:model-value="setCORSList('exposed_headers', $event)"
                />
              </template>
            </CompactFieldError>
          </div>

          <div class="browser-access__field">
            <span>{{ t('settings.browserAccess.cors.maxAge') }}</span>
            <CompactFieldError id="settings-value-cors-max-age" :error="maxAgeError">
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  id="settings-value-cors-max-age"
                  type="number"
                  :model-value="String(cors.max_age)"
                  :label="t('settings.browserAccess.cors.maxAge')"
                  appearance="surface"
                  size="compact"
                  monospace
                  min="0"
                  step="1"
                  inputmode="numeric"
                  :disabled="disabled"
                  :invalid="invalid"
                  :described-by="describedBy"
                  @update:model-value="setMaxAge"
                />
              </template>
            </CompactFieldError>
          </div>

          <div class="browser-access__switch-field">
            <div>
              <strong>{{ t('settings.browserAccess.cors.allowCredentials') }}</strong>
              <small>{{ t('settings.browserAccess.cors.allowCredentialsHelp') }}</small>
            </div>
            <AppSwitch
              :model-value="cors.allow_credentials"
              :disabled="disabled"
              :label="t('settings.browserAccess.cors.allowCredentials')"
              @update:model-value="setAllowCredentials"
            />
          </div>

          <p class="browser-access__notice" role="note">
            {{ t('settings.browserAccess.cors.securityNotice') }}
          </p>
        </div>
        <p v-else class="browser-access__summary">
          {{
            cors.enabled
              ? t('settings.browserAccess.cors.enabledSummary', {
                  count: cors.allowed_origins.length,
                })
              : t('settings.browserAccess.cors.disabledSummary')
          }}
        </p>
      </SettingBlock>

      <SettingBlock
        :title="t('settings.headers.blockTitle')"
        :help="t('settings.headers.description')"
        :meta="t('settings.headers.ruleCount', { count: headerRuleCount })"
        :source-label="sourceLabel(headerRulesOverridden, headerRulesPendingRestore)"
        :action-label="
          headerRulesOverridden
            ? t('settings.runtime.restoreDefault')
            : t('settings.runtime.override')
        "
        :overridden="headerRulesOverridden"
        :pending-restore="headerRulesPendingRestore"
        :disabled="disabled"
        @toggle="toggleOverride('header_rules')"
      >
        <HeaderRulesEditor
          appearance="ledger"
          :model-value="headerRules"
          :disabled="disabled || !headerRulesOverridden"
          :reset-key="headerRulesEditorResetKey"
          :show-notice="false"
          :show-add="headerRulesOverridden"
          @update:model-value="updateHeaderRules"
          @update:valid="headerRulesRawValid = $event"
          @update:invalid-edits="headerRulesInvalidEdits = $event"
        />
      </SettingBlock>

      <SettingBlock
        :title="t('settings.browserAccess.responseHeaders.title')"
        :help="t('settings.browserAccess.responseHeaders.description')"
        :meta="t('settings.headers.ruleCount', { count: responseRuleCount })"
        :source-label="sourceLabel(responseRulesOverridden, responseRulesPendingRestore)"
        :action-label="
          responseRulesOverridden
            ? t('settings.runtime.restoreDefault')
            : t('settings.runtime.override')
        "
        :overridden="responseRulesOverridden"
        :pending-restore="responseRulesPendingRestore"
        :disabled="disabled"
        @toggle="toggleOverride('response_header_rules')"
      >
        <HeaderRulesEditor
          appearance="ledger"
          validation-policy="response"
          :model-value="responseRules"
          :disabled="disabled || !responseRulesOverridden"
          :reset-key="responseEditorResetKey"
          :show-notice="false"
          :show-add="responseRulesOverridden"
          :remove-hint="t('settings.browserAccess.responseHeaders.removeHint')"
          @update:model-value="updateResponseRules"
          @update:valid="responseRulesValid = $event"
          @update:invalid-edits="responseRulesInvalidEdits = $event"
        />
      </SettingBlock>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.browser-access__blocks,
.browser-access__field,
.browser-access__switch-field {
  display: grid;
}

.settings-section {
  gap: var(--space-4);
  scroll-margin-top: 76px;
}

.settings-section__heading h2,
.settings-section__heading p,
.browser-access__summary,
.browser-access__notice {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--title-section);
  font-weight: 650;
}

.settings-section__heading p,
.browser-access__summary {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.browser-access__blocks {
  gap: var(--space-5);
}

.browser-access__switch-field strong {
  font-size: var(--text-meta);
}

.browser-access__cors-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3) var(--space-4);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: var(--space-3) var(--space-4);
}

.browser-access__field {
  min-width: 0;
  gap: var(--space-1);
}

.browser-access__field > span {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.browser-access__field--wide,
.browser-access__notice {
  grid-column: 1 / -1;
}

.browser-access__switch-field {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
}

.browser-access__switch-field div {
  display: grid;
  gap: var(--space-1);
}

.browser-access__switch-field small {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.browser-access__notice {
  border: 1px solid color-mix(in srgb, var(--color-warning) 34%, var(--color-border-subtle));
  border-radius: var(--radius-control);
  background: var(--color-warning-bg);
  color: var(--color-warning);
  padding: 10px 12px;
  font-size: var(--text-sm);
  line-height: 1.5;
}

@media (max-width: 800px) {
  .browser-access__cors-form {
    grid-template-columns: 1fr;
  }

  .browser-access__field--wide,
  .browser-access__notice {
    grid-column: auto;
  }
}
</style>
