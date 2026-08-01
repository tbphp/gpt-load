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

      <FormField
        id="import-upstream-url"
        :label="t('import.connection.url')"
        :description="t('import.connection.urlDescription')"
        :error="urlError"
        required
        :required-text="t('import.required')"
        size="compact"
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
  padding: 22px 0 var(--space-6);
}

.import-connection > header h2 {
  margin: 0;
}

.import-connection > header h2 {
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 500;
}

.import-connection__fields {
  display: grid;
  grid-template-columns: minmax(220px, 0.72fr) minmax(340px, 1.28fr);
  gap: 18px;
  margin-top: var(--space-3);
}

.import-connection__url {
  font-family: var(--font-mono);
}

.import-connection__protocols {
  display: grid;
  gap: var(--space-2);
  margin: 18px 0 0;
  border: 0;
  padding: 0;
}

.import-connection__protocols legend {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
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
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-2);
}

.import-connection__protocol-grid label {
  display: flex;
  min-height: 58px;
  align-items: flex-start;
  gap: 9px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 9px 10px;
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

.import-connection__protocol-grid label:has(input:disabled) {
  cursor: not-allowed;
  opacity: 0.55;
}

.import-connection__protocol-grid input {
  position: relative;
  display: grid;
  width: 18px;
  height: 18px;
  flex: none;
  place-items: center;
  margin: 0;
  border: 1px solid var(--color-border-control);
  border-radius: 4px;
  appearance: none;
  background: var(--color-surface);
  cursor: inherit;
}

.import-connection__protocol-grid input:checked {
  border-color: var(--color-action);
  background: var(--color-action);
}

.import-connection__protocol-grid input:checked::after {
  color: var(--color-action-ink);
  content: '✓';
  font-size: 11px;
  font-weight: 800;
  line-height: 1;
}

.import-connection__protocol-grid input:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.import-connection__protocol-grid strong,
.import-connection__protocol-grid small {
  display: block;
}

.import-connection__protocol-grid strong {
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}

.import-connection__protocol-grid small {
  margin-top: 2px;
  color: var(--color-text-faint);
  font-size: 9.8px;
  line-height: 1.55;
}

@media (max-width: 860px) {
  .import-connection__fields {
    grid-template-columns: 1fr;
  }

  .import-connection__protocol-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 520px) {
  .import-connection__protocol-grid {
    grid-template-columns: 1fr;
  }
}
</style>
