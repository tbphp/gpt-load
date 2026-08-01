<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { protocolCatalog } from '@/api/control/protocols'
import type { GroupProtocol } from '@/api/control/types'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

defineProps<{
  name: string
  upstreamUrl: string
  protocols: readonly GroupProtocol[]
  urlError: string
  protocolsError: string
  disabled?: boolean
}>()
const emit = defineEmits<{
  'update:name': [value: string]
  'update:upstreamUrl': [value: string]
  'update:protocols': [value: GroupProtocol[]]
}>()
const { t } = useI18n()

function toggleProtocol(
  current: readonly GroupProtocol[],
  protocol: GroupProtocol,
  checked: boolean,
): void {
  emit(
    'update:protocols',
    checked
      ? protocolCatalog
          .map(({ value }) => value)
          .filter((value) => [...current, protocol].includes(value))
      : current.filter((value) => value !== protocol),
  )
}
</script>

<template>
  <section class="import-connection" aria-labelledby="import-connection-heading">
    <header>
      <h2 id="import-connection-heading">{{ t('import.connection.title') }}</h2>
      <p>{{ t('import.connection.description') }}</p>
    </header>

    <div class="import-connection__fields">
      <FormField id="import-group-name" :label="t('import.connection.name')">
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

      <FormField
        id="import-upstream-url"
        :label="t('import.connection.url')"
        :description="t('import.connection.urlDescription')"
        :error="urlError"
        required
        :required-text="t('import.required')"
      >
        <template #default="field">
          <input
            id="import-upstream-url"
            class="import-connection__url"
            :value="upstreamUrl"
            type="url"
            :disabled="disabled"
            :aria-invalid="field.invalid || undefined"
            :aria-describedby="field.describedBy"
            autocomplete="off"
            autocapitalize="none"
            spellcheck="false"
            @input="emit('update:upstreamUrl', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>
    </div>

    <fieldset class="import-connection__protocols">
      <legend>
        {{ t('import.connection.protocols') }}
        <span>{{ t('import.connection.protocolsMultiple') }}</span>
      </legend>
      <div class="import-connection__protocol-grid">
        <label v-for="protocol in protocolCatalog" :key="protocol.value">
          <input
            type="checkbox"
            :checked="protocols.includes(protocol.value)"
            :disabled="disabled"
            @change="
              toggleProtocol(protocols, protocol.value, ($event.target as HTMLInputElement).checked)
            "
          />
          <span>
            <strong>{{ protocol.value }}</strong>
            <small>{{ t(`import.connection.protocolDescriptions.${protocol.value}`) }}</small>
          </span>
        </label>
      </div>
      <InlineFeedback v-if="protocolsError" tone="danger" appearance="hint">
        {{ protocolsError }}
      </InlineFeedback>
    </fieldset>
  </section>
</template>

<style scoped>
.import-connection {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-5) 0 var(--space-6);
}

.import-connection > header h2,
.import-connection > header p {
  margin: 0;
}

.import-connection > header h2 {
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 500;
}

.import-connection > header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.import-connection__fields {
  display: grid;
  grid-template-columns: minmax(0, 0.8fr) minmax(0, 1.2fr);
  gap: var(--space-4);
  margin-top: var(--space-4);
}

.import-connection__url {
  font-family: var(--font-mono);
}

.import-connection__protocols {
  display: grid;
  gap: var(--space-2);
  margin: var(--space-5) 0 0;
  border: 0;
  padding: 0;
}

.import-connection__protocols legend {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}

.import-connection__protocols legend span {
  color: var(--color-text-faint);
  font-weight: 400;
}

.import-connection__protocol-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}

.import-connection__protocol-grid label {
  display: flex;
  min-height: var(--touch-target);
  align-items: flex-start;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: var(--space-2) var(--space-3);
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}

.import-connection__protocol-grid label:hover:not(:has(input:disabled)) {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
}

.import-connection__protocol-grid label:has(input:checked) {
  border-color: var(--color-action);
  background: var(--color-action-soft);
}

.import-connection__protocol-grid label:has(input:disabled) {
  cursor: not-allowed;
  opacity: 0.55;
}

.import-connection__protocol-grid input {
  width: var(--space-4);
  height: var(--space-4);
  flex: none;
  margin-top: var(--space-1);
  accent-color: var(--color-action);
}

.import-connection__protocol-grid strong,
.import-connection__protocol-grid small {
  display: block;
}

.import-connection__protocol-grid strong {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

.import-connection__protocol-grid small {
  margin-top: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: var(--line-normal);
}

@media (max-width: 680px) {
  .import-connection__fields,
  .import-connection__protocol-grid {
    grid-template-columns: 1fr;
  }
}
</style>
