<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HeaderRulesDto } from '@/app/resources/groups'
import type { RuntimeSettingKey, SettingsResource } from '@/app/resources/settings'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import {
  createSettingsDraft,
  isValidCORSConfig,
  setSettingsOverride,
  type SettingsDraft,
} from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

type CORSListKey = 'allowed_origins' | 'allowed_methods' | 'allowed_headers' | 'exposed_headers'

const props = defineProps<{
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  resetKey: number
}>()
const emit = defineEmits<{
  change: [change: SettingsDraftChange]
  'update:valid': [value: boolean]
  'update:corsValid': [value: boolean]
  'update:responseRulesValid': [value: boolean]
  'update:invalidEdits': [value: boolean]
}>()
const { t } = useI18n()
const responseRulesValid = ref(true)
const responseRulesInvalidEdits = ref(false)
const responseEditorResetKey = ref(0)

const corsOverridden = computed(() => props.draft.overrides.has('cors'))
const responseRulesOverridden = computed(() => props.draft.overrides.has('response_header_rules'))
const corsPendingRestore = computed(
  () => !corsOverridden.value && props.base.settings.overrides.includes('cors'),
)
const responseRulesPendingRestore = computed(
  () =>
    !responseRulesOverridden.value &&
    props.base.settings.overrides.includes('response_header_rules'),
)
const cors = computed(() =>
  corsOverridden.value ? props.draft.values.cors : props.base.settings.values.cors,
)
const responseRules = computed(() =>
  responseRulesOverridden.value
    ? props.draft.values.response_header_rules
    : props.base.settings.values.response_header_rules,
)
const responseRuleCount = computed(
  () => Object.keys(responseRules.value.set).length + responseRules.value.remove.length,
)
const corsValid = computed(
  () => !corsOverridden.value || isValidCORSConfig(props.draft.values.cors),
)
const effectiveResponseRulesValid = computed(
  () => !responseRulesOverridden.value || responseRulesValid.value,
)
const valid = computed(() => corsValid.value && effectiveResponseRulesValid.value)

watch(valid, (value) => emit('update:valid', value), { immediate: true })
watch(corsValid, (value) => emit('update:corsValid', value), { immediate: true })
watch(effectiveResponseRulesValid, (value) => emit('update:responseRulesValid', value), {
  immediate: true,
})
watch(responseRulesInvalidEdits, (value) => emit('update:invalidEdits', value), { immediate: true })
watch(
  () => props.resetKey,
  () => {
    responseRulesValid.value = true
    responseRulesInvalidEdits.value = false
    responseEditorResetKey.value += 1
  },
)
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

async function toggleOverride(key: 'cors' | 'response_header_rules'): Promise<void> {
  publish(
    key,
    setSettingsOverride(props.base.settings, props.draft, key, !props.draft.overrides.has(key)),
  )
  if (key === 'response_header_rules') {
    responseRulesValid.value = true
    responseRulesInvalidEdits.value = false
    await nextTick()
    responseEditorResetKey.value += 1
  }
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
      <article class="browser-access__block">
        <header class="browser-access__block-heading">
          <div class="browser-access__identity">
            <strong>{{ t('settings.browserAccess.cors.title') }}</strong>
            <small>{{ t('settings.browserAccess.cors.description') }}</small>
          </div>
          <div class="browser-access__meta">
            <StatusBadge
              size="compact"
              :tone="corsPendingRestore ? 'warning' : corsOverridden ? 'info' : 'neutral'"
              :icon="corsPendingRestore ? 'alert' : corsOverridden ? 'edit' : 'check'"
            >
              {{ sourceLabel(corsOverridden, corsPendingRestore) }}
            </StatusBadge>
            <AppButton
              variant="secondary"
              :tone="corsOverridden ? 'warning' : 'action'"
              size="compact"
              :disabled="disabled"
              @click="toggleOverride('cors')"
            >
              {{
                corsOverridden
                  ? t('settings.runtime.restoreDefault')
                  : t('settings.runtime.override')
              }}
            </AppButton>
          </div>
        </header>

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
      </article>

      <article class="browser-access__block">
        <header class="browser-access__block-heading">
          <div class="browser-access__identity">
            <strong>{{ t('settings.browserAccess.responseHeaders.title') }}</strong>
            <small>{{ t('settings.browserAccess.responseHeaders.description') }}</small>
          </div>
          <div class="browser-access__meta">
            <span>{{ t('settings.headers.ruleCount', { count: responseRuleCount }) }}</span>
            <StatusBadge
              size="compact"
              :tone="
                responseRulesPendingRestore
                  ? 'warning'
                  : responseRulesOverridden
                    ? 'info'
                    : 'neutral'
              "
              :icon="
                responseRulesPendingRestore ? 'alert' : responseRulesOverridden ? 'edit' : 'check'
              "
            >
              {{ sourceLabel(responseRulesOverridden, responseRulesPendingRestore) }}
            </StatusBadge>
            <AppButton
              variant="secondary"
              :tone="responseRulesOverridden ? 'warning' : 'action'"
              size="compact"
              :disabled="disabled"
              @click="toggleOverride('response_header_rules')"
            >
              {{
                responseRulesOverridden
                  ? t('settings.runtime.restoreDefault')
                  : t('settings.runtime.override')
              }}
            </AppButton>
          </div>
        </header>

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
      </article>
    </div>
  </section>
</template>

<style scoped>
.settings-section,
.settings-section__heading,
.browser-access__blocks,
.browser-access__block,
.browser-access__identity,
.browser-access__meta,
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
.browser-access__identity strong,
.browser-access__identity small,
.browser-access__summary,
.browser-access__notice {
  margin: 0;
}

.settings-section__heading h2 {
  font-size: var(--text-body);
  font-weight: 650;
}

.settings-section__heading p,
.browser-access__identity small,
.browser-access__summary {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.browser-access__blocks {
  gap: var(--space-5);
}

.browser-access__block {
  gap: var(--space-3);
}

.browser-access__block + .browser-access__block {
  border-top: 1px dashed var(--color-border-subtle);
  padding-top: var(--space-4);
}

.browser-access__block-heading {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: var(--space-4);
}

.browser-access__identity {
  gap: var(--space-1);
}

.browser-access__identity strong,
.browser-access__switch-field strong {
  font-size: var(--text-meta);
}

.browser-access__meta {
  justify-items: end;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  text-align: end;
}

.browser-access__cors-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3) var(--space-4);
  border-left: 2px solid var(--color-border-subtle);
  padding: var(--space-2) 0 var(--space-2) var(--space-4);
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
  .browser-access__block-heading,
  .browser-access__cors-form {
    grid-template-columns: 1fr;
  }

  .browser-access__meta {
    justify-items: start;
    text-align: start;
  }

  .browser-access__field--wide,
  .browser-access__notice {
    grid-column: auto;
  }
}
</style>
