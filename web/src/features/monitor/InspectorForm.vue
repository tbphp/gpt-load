<script setup lang="ts">
import { Info } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import MonitorSectionHeading from './MonitorSectionHeading.vue'

type InspectorField = 'protocol' | 'operation' | 'externalModel' | 'accessKey'
type InspectorErrors = Partial<Record<InspectorField, string>>
interface SelectOption {
  value: string
  label: string
}

const props = defineProps<{
  protocol: string
  operation: string
  routeRequirement: string
  model: string
  accessKeyId: string
  protocolOptions: SelectOption[]
  operationOptions: SelectOption[]
  routeRequirementOptions: SelectOption[]
  modelRequired: boolean
  modelAllowed: boolean
  accessKeyOptions: SelectOption[]
  errors: InspectorErrors
  optionsPending: boolean
  optionsFailed: boolean
  missingAccessKey: boolean
  submitPending: boolean
}>()
const emit = defineEmits<{
  'update:protocol': [value: string]
  'update:operation': [value: string]
  'update:routeRequirement': [value: string]
  'update:model': [value: string]
  'update:accessKeyId': [value: string]
  submit: []
  retryOptions: []
}>()
const { t } = useI18n()
const hasValidationError = computed(() => Object.keys(props.errors).length > 0)

function error(field: InspectorField): string | undefined {
  const key = props.errors[field]
  return key ? t(key) : undefined
}
</script>

<template>
  <aside class="inspector-form-panel" aria-labelledby="inspector-form-title">
    <MonitorSectionHeading id="inspector-form-title" :title="t('monitor.inspector.form.title')" />

    <div class="inspector-form-panel__body">
      <AsyncRefreshIndicator
        :active="optionsPending"
        :label="t('monitor.inspector.options.loading')"
      />
      <QueryFeedback
        v-if="optionsFailed"
        state="error"
        :message="t('monitor.inspector.options.failed')"
        :retry-label="t('common.retry')"
        @retry="emit('retryOptions')"
      />

      <form
        class="inspector-form"
        :aria-label="t('monitor.inspector.form.label')"
        @submit.prevent="emit('submit')"
      >
        <FormField
          id="inspector-access-key"
          size="compact"
          :label="t('monitor.inspector.form.accessKey')"
          :error="error('accessKey')"
          required
        >
          <template #default="{ describedBy }">
            <AppSelect
              id="inspector-access-key"
              :model-value="accessKeyId"
              :label="t('monitor.inspector.form.accessKey')"
              :options="accessKeyOptions"
              :aria-describedby="describedBy"
              :aria-invalid="error('accessKey') ? 'true' : undefined"
              size="compact"
              @update:model-value="emit('update:accessKeyId', $event)"
            />
          </template>
        </FormField>

        <FormField
          id="inspector-operation"
          size="compact"
          :label="t('monitor.inspector.form.operation')"
          :error="error('operation')"
          required
        >
          <template #default="{ describedBy }">
            <AppSelect
              id="inspector-operation"
              :model-value="operation"
              :label="t('monitor.inspector.form.operation')"
              :options="operationOptions"
              :aria-describedby="describedBy"
              :aria-invalid="error('operation') ? 'true' : undefined"
              size="compact"
              @update:model-value="emit('update:operation', $event)"
            />
          </template>
        </FormField>

        <FormField
          id="inspector-protocol"
          size="compact"
          :label="t('monitor.inspector.form.protocol')"
          :error="error('protocol')"
          required
        >
          <template #default="{ describedBy }">
            <AppSelect
              id="inspector-protocol"
              :model-value="protocol"
              :label="t('monitor.inspector.form.protocol')"
              :options="protocolOptions"
              :aria-describedby="describedBy"
              :aria-invalid="error('protocol') ? 'true' : undefined"
              size="compact"
              @update:model-value="emit('update:protocol', $event)"
            />
          </template>
        </FormField>

        <FormField
          id="inspector-model"
          size="compact"
          :label="t('monitor.inspector.form.model')"
          :label-suffix="modelRequired ? undefined : t('monitor.inspector.form.optional')"
          :error="error('externalModel')"
          :required="modelRequired"
        >
          <template #default="{ describedBy }">
            <input
              id="inspector-model"
              :value="model"
              type="text"
              autocomplete="off"
              :placeholder="t('monitor.inspector.form.modelPlaceholder')"
              :aria-describedby="describedBy"
              :aria-invalid="error('externalModel') ? 'true' : undefined"
              :disabled="!modelAllowed"
              @input="emit('update:model', ($event.target as HTMLInputElement).value)"
            />
          </template>
        </FormField>

        <FormField
          id="inspector-route-requirement"
          size="compact"
          :label="t('monitor.inspector.form.routeRequirement')"
          required
        >
          <template #default="{ describedBy }">
            <AppSelect
              id="inspector-route-requirement"
              :model-value="routeRequirement"
              :label="t('monitor.inspector.form.routeRequirement')"
              :options="routeRequirementOptions"
              :aria-describedby="describedBy"
              size="compact"
              @update:model-value="emit('update:routeRequirement', $event)"
            />
          </template>
        </FormField>

        <AppButton
          class="inspector-form__submit"
          type="submit"
          size="compact"
          :busy="submitPending"
          :disabled="optionsPending || optionsFailed"
        >
          {{ t('monitor.inspector.form.submit') }}
        </AppButton>
      </form>

      <p v-if="missingAccessKey" class="inspector-inline-error" role="alert">
        {{ t('monitor.inspector.errors.missingDeepLinkAccessKey', { id: accessKeyId }) }}
      </p>
      <p v-if="hasValidationError" class="sr-only" role="alert">
        {{ t('monitor.inspector.errors.summary') }}
      </p>

      <InlineFeedback tone="neutral" appearance="ledger" glyph="">
        <template #glyph><Info :size="14" aria-hidden="true" /></template>
        {{ t('monitor.inspector.boundary') }}
      </InlineFeedback>
    </div>
  </aside>
</template>

<style scoped>
.inspector-form-panel {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}

.inspector-form-panel__body,
.inspector-form {
  display: grid;
  min-width: 0;
}

.inspector-form-panel__body {
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  gap: var(--space-4);
  padding: 18px;
}

.inspector-form {
  gap: var(--space-3);
}

.inspector-form :deep(.app-select__trigger) {
  width: 100%;
  min-height: var(--control-xs);
  height: var(--control-xs);
  background: var(--color-surface);
  color: var(--color-text);
  font-size: var(--text-meta);
}

.inspector-form :deep(input) {
  font-family: var(--font-mono);
}

.inspector-form__submit {
  width: 100%;
  margin-top: var(--space-1);
}

.inspector-inline-error {
  margin: 0;
  border: 1px solid color-mix(in srgb, var(--color-danger) 34%, var(--color-border-subtle));
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  color: var(--color-danger);
  padding: 9px 10px;
  font-size: var(--text-sm);
}

.inspector-form-panel :deep(.query-feedback) {
  min-height: 0;
  padding: 9px 10px;
  font-size: var(--text-sm);
}

@media (max-width: 1120px) {
  .inspector-form-panel__body {
    gap: var(--space-3);
  }

  .inspector-form {
    grid-template-columns: repeat(3, minmax(0, 1fr)) auto;
    align-items: end;
  }

  .inspector-form__submit {
    width: auto;
    margin-top: 0;
  }

  .inspector-form-panel__body > :deep(.inline-feedback) {
    grid-column: 1 / -1;
  }
}

@media (max-width: 760px) {
  .inspector-form {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .inspector-form__submit {
    width: 100%;
  }
}

@media (max-width: 520px) {
  .inspector-form-panel__header,
  .inspector-form-panel__body {
    padding-inline: 14px;
  }

  .inspector-form {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
