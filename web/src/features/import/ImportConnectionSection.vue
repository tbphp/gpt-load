<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProxyConfiguredMode } from '@/api/control/types'
import type { ChannelDto } from '@/app/resources/channels'
import { proxyMutation } from '@/app/resources/proxy'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'
import { hasUpstreamBaseURLVersionMismatch, isValidUpstreamBaseURL } from '@/lib/upstream-base-url'

import type { ImportProxyDraft } from './model-draft'

const props = defineProps<{
  channel: ChannelDto | null
  name: string
  params: Record<string, string>
  proxy: ImportProxyDraft
  paramErrors: Readonly<Record<string, string>>
  baseUrlOverrideEnabled: boolean
  disabled?: boolean
  proxyDisabled?: boolean
}>()
const emit = defineEmits<{
  'update:name': [value: string]
  'update:param': [key: string, value: string]
  'update:proxy': [value: ImportProxyDraft]
  'update:base-url-override': [enabled: boolean]
  'blur:param': [key: string]
}>()
const { t } = useI18n()
const proxySupported = computed(() => props.channel?.capabilities.outbound_proxy === true)
const proxyModeOptions = computed(() => [
  { value: 'inherit', label: t('common.proxy.inherit.group') },
  { value: 'direct', label: t('common.proxy.mode.direct') },
  { value: 'custom', label: t('common.proxy.mode.custom') },
])
const proxyError = computed(() =>
  proxySupported.value &&
  props.proxy.mode === 'custom' &&
  proxyMutation(props.proxy.mode, props.proxy.url) === undefined
    ? t('common.proxy.invalid')
    : undefined,
)
const proxyDescription = computed(() => {
  if (!proxySupported.value || props.proxy.mode === 'custom') return undefined
  return t(`common.proxy.help.${props.proxy.mode}`)
})

function updateProxyMode(value: string): void {
  if (!['inherit', 'direct', 'custom'].includes(value)) return
  emit('update:proxy', { mode: value as ProxyConfiguredMode, url: '' })
}

function updateProxyURL(value: string): void {
  if (props.proxy.mode !== 'custom') return
  emit('update:proxy', { mode: 'custom', url: value })
}

function fieldError(key: string): string {
  return props.paramErrors[key] ?? ''
}

function isOptionalBaseURL(key: string, required: boolean): boolean {
  return key === 'base_url' && !required
}

function baseURLDescription(): string {
  if (props.channel?.channel_id === 'newapi') {
    return t('import.connection.newApiUrlDescription')
  }
  if (props.channel?.channel_id === 'openai_compatible') {
    return t('import.connection.compatibleUrlDescription')
  }
  if (!props.channel?.default_base_url) return t('import.connection.urlDescription')
  return t('import.connection.urlDescriptionWithDefault', {
    url: props.channel.default_base_url,
  })
}

function baseURLVersionWarning(key: string): string | undefined {
  if (key !== 'base_url' || !props.channel?.default_base_url) return undefined
  const value = props.params[key]?.trim() ?? ''
  if (!value || !isValidUpstreamBaseURL(value)) return undefined
  return hasUpstreamBaseURLVersionMismatch(props.channel.default_base_url, value)
    ? t('import.connection.urlVersionWarning')
    : undefined
}
</script>

<template>
  <div class="import-connection">
    <div class="import-connection__fields">
      <FormField
        id="import-group-name"
        class="import-connection__name"
        :label="t('import.connection.name')"
        :label-suffix="t('import.optional')"
        size="compact"
      >
        <template #default="field">
          <input
            id="import-group-name"
            :value="name"
            :disabled="disabled"
            :aria-describedby="field.describedBy"
            autocomplete="off"
            :placeholder="t('import.connection.namePlaceholder')"
            @input="emit('update:name', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>

      <div v-if="channel?.param_fields.length" class="import-connection__params">
        <template v-for="param in channel.param_fields" :key="param.key">
          <FormField
            v-if="isOptionalBaseURL(param.key, param.required)"
            id="import-channel-base-url-override"
            class="import-connection__param import-connection__param--optional-url"
            :label="t('import.connection.customUrl')"
            :description="baseURLDescription()"
            :description-warning="baseURLVersionWarning(param.key)"
            :error="fieldError(param.key)"
            :required="baseUrlOverrideEnabled"
            :required-text="t('import.required')"
            size="compact"
          >
            <template #default="field">
              <div class="import-connection__switch-row">
                <AppSwitch
                  id="import-channel-base-url-override"
                  :model-value="baseUrlOverrideEnabled"
                  :disabled="disabled"
                  :label="t('import.connection.customUrl')"
                  @update:model-value="emit('update:base-url-override', $event)"
                />
                <div v-if="baseUrlOverrideEnabled" class="import-connection__url-input">
                  <input
                    :id="`import-channel-param-${param.key}`"
                    class="import-connection__url"
                    :value="params[param.key] ?? ''"
                    type="url"
                    required
                    :disabled="disabled"
                    :aria-label="t('import.connection.customUrl')"
                    :aria-invalid="field.invalid || undefined"
                    :aria-describedby="field.describedBy"
                    autocomplete="off"
                    autocapitalize="none"
                    spellcheck="false"
                    placeholder="https://"
                    @input="
                      emit('update:param', param.key, ($event.target as HTMLInputElement).value)
                    "
                    @blur="emit('blur:param', param.key)"
                  />
                </div>
              </div>
            </template>
          </FormField>

          <FormField
            v-else
            :id="`import-channel-param-${param.key}`"
            class="import-connection__param"
            :label="param.key === 'base_url' ? t('import.connection.url') : param.label"
            :description="param.key === 'base_url' ? baseURLDescription() : undefined"
            :description-warning="
              param.key === 'base_url' ? baseURLVersionWarning(param.key) : undefined
            "
            :error="fieldError(param.key)"
            :required="param.required"
            :required-text="t('import.required')"
            size="compact"
          >
            <template #default="field">
              <input
                :id="`import-channel-param-${param.key}`"
                :class="{ 'import-connection__url': param.input_kind === 'url' }"
                :value="params[param.key] ?? ''"
                :type="param.input_kind === 'url' ? 'url' : 'text'"
                :disabled="disabled"
                :aria-invalid="field.invalid || undefined"
                :aria-describedby="field.describedBy"
                autocomplete="off"
                autocapitalize="none"
                spellcheck="false"
                :placeholder="param.input_kind === 'url' ? 'https://' : undefined"
                @input="emit('update:param', param.key, ($event.target as HTMLInputElement).value)"
                @blur="emit('blur:param', param.key)"
              />
            </template>
          </FormField>
        </template>
      </div>

      <FormField
        v-if="channel"
        id="import-group-proxy-mode"
        class="import-connection__proxy"
        :label="t('common.proxy.title')"
        :description="proxyDescription"
        :disabled-reason="
          !proxySupported
            ? t('common.proxy.unsupportedHelp')
            : proxyDisabled
              ? t('import.connection.proxyLocked')
              : undefined
        "
        :error="proxyError"
        size="compact"
      >
        <template #default="field">
          <div class="import-connection__proxy-controls">
            <AppSelect
              id="import-group-proxy-mode"
              :model-value="proxySupported ? proxy.mode : 'inherit'"
              :label="t('common.proxy.modeLabel')"
              :options="proxyModeOptions"
              size="sm"
              :disabled="disabled || proxyDisabled || !proxySupported"
              @update:model-value="updateProxyMode"
            />
            <AppTextInput
              v-if="proxySupported && proxy.mode === 'custom'"
              id="import-group-proxy-url"
              :model-value="proxy.url"
              :label="t('common.proxy.urlLabel')"
              :placeholder="t('common.proxy.placeholder')"
              appearance="surface"
              size="sm"
              autocomplete="off"
              :spellcheck="false"
              monospace
              :disabled="disabled || proxyDisabled"
              :invalid="field.invalid"
              :described-by="field.describedBy"
              @update:model-value="updateProxyURL"
            />
          </div>
        </template>
      </FormField>
    </div>
  </div>
</template>

<style scoped>
.import-connection {
  min-width: 0;
  margin-top: var(--space-5);
}

.import-connection__fields {
  display: grid;
  grid-template-columns: minmax(180px, 260px) minmax(0, 1fr);
  align-items: start;
  gap: 18px;
}

.import-connection__name,
.import-connection__params,
.import-connection__param,
.import-connection__proxy {
  min-width: 0;
}

.import-connection__params {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 280px), 1fr));
  gap: var(--space-4);
}

.import-connection__proxy {
  grid-column: 1 / -1;
}

.import-connection__proxy-controls {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.import-connection__proxy-controls :deep(.app-text-input) {
  flex: 1 1 auto;
}

.import-connection__url {
  font-family: var(--font-mono);
}

.import-connection__switch-row {
  display: flex;
  min-height: var(--control-xs);
  align-items: center;
  gap: var(--space-3);
}

.import-connection__url-input {
  min-width: 0;
  flex: 1 1 auto;
}

@media (max-width: 860px) {
  .import-connection__fields {
    grid-template-columns: minmax(0, 1fr);
  }

  .import-connection__switch-row {
    min-height: var(--touch-target);
  }

  .import-connection__proxy-controls {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
