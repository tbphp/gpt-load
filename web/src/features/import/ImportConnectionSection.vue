<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { ChannelDto } from '@/app/resources/channels'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import FormField from '@/components/ui/FormField.vue'
import { hasUpstreamBaseURLVersionMismatch, isValidUpstreamBaseURL } from '@/lib/upstream-base-url'

const props = defineProps<{
  channel: ChannelDto | null
  name: string
  params: Record<string, string>
  paramErrors: Readonly<Record<string, string>>
  baseUrlOverrideEnabled: boolean
  disabled?: boolean
}>()
const emit = defineEmits<{
  'update:name': [value: string]
  'update:param': [key: string, value: string]
  'update:base-url-override': [enabled: boolean]
  'blur:param': [key: string]
}>()
const { t } = useI18n()

function fieldError(key: string): string {
  return props.paramErrors[key] ?? ''
}

function isOptionalBaseURL(key: string, required: boolean): boolean {
  return key === 'base_url' && !required
}

function baseURLDescription(): string {
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
.import-connection__param {
  min-width: 0;
}

.import-connection__params {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 280px), 1fr));
  gap: var(--space-4);
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
}
</style>
