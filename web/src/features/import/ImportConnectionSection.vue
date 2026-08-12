<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { ChannelDto } from '@/app/resources/channels'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import FormField from '@/components/ui/FormField.vue'

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
</script>

<template>
  <section class="import-connection" aria-labelledby="import-connection-heading">
    <header>
      <h2 id="import-connection-heading">{{ t('import.connection.title') }}</h2>
    </header>

    <div class="import-connection__fields">
      <FormField
        id="import-group-name"
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

      <template v-for="param in channel?.param_fields ?? []" :key="param.key">
        <FormField
          v-if="isOptionalBaseURL(param.key, param.required)"
          id="import-channel-base-url-override"
          :label="t('import.connection.customUrl')"
          :description="t('import.connection.customUrlDescription')"
          size="compact"
        >
          <template #default>
            <div class="import-connection__switch-row">
              <AppSwitch
                :model-value="baseUrlOverrideEnabled"
                :disabled="disabled"
                :label="t('import.connection.customUrl')"
                @update:model-value="emit('update:base-url-override', $event)"
              />
            </div>
          </template>
        </FormField>

        <FormField
          v-if="!isOptionalBaseURL(param.key, param.required) || baseUrlOverrideEnabled"
          :id="`import-channel-param-${param.key}`"
          :label="param.key === 'base_url' ? t('import.connection.url') : param.label"
          :description="param.key === 'base_url' ? baseURLDescription() : undefined"
          :error="fieldError(param.key)"
          :required="param.required || (param.key === 'base_url' && baseUrlOverrideEnabled)"
          :required-text="t('import.required')"
          size="compact"
        >
          <template #default="field">
            <input
              :id="`import-channel-param-${param.key}`"
              class="import-connection__url"
              :value="params[param.key] ?? ''"
              :type="param.input_kind === 'url' ? 'url' : 'text'"
              :disabled="disabled"
              :aria-invalid="field.invalid || undefined"
              :aria-describedby="field.describedBy"
              autocomplete="off"
              autocapitalize="none"
              spellcheck="false"
              @input="emit('update:param', param.key, ($event.target as HTMLInputElement).value)"
            />
          </template>
        </FormField>
      </template>
    </div>
  </section>
</template>

<style scoped>
.import-connection {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
}

.import-connection > header h2 {
  margin: 0;
}

.import-connection > header h2 {
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
}

.import-connection__fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: start;
  gap: 18px;
  margin-top: var(--space-3);
}

.import-connection__url {
  font-family: var(--font-mono);
}

.import-connection__switch-row {
  display: flex;
  min-height: var(--control-xs);
  align-items: center;
}

@media (max-width: 860px) {
  .import-connection__fields {
    grid-template-columns: 1fr;
  }
}
</style>
