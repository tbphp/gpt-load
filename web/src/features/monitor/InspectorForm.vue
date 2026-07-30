<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

type InspectorField = 'protocol' | 'externalModel' | 'accessKey'
type InspectorErrors = Partial<Record<InspectorField, string>>
interface SelectOption {
  value: string
  label: string
}

const props = defineProps<{
  protocol: string
  model: string
  accessKeyId: string
  protocolOptions: SelectOption[]
  accessKeyOptions: SelectOption[]
  errors: InspectorErrors
  optionsPending: boolean
  optionsFailed: boolean
  missingAccessKey: boolean
}>()
const emit = defineEmits<{
  'update:protocol': [value: string]
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
  <SurfaceCard class="inspector-form-card">
    <header class="inspector-heading">
      <div>
        <h2>{{ t('monitor.inspector.title') }}</h2>
        <p>{{ t('monitor.inspector.description') }}</p>
      </div>
    </header>

    <p class="inspector-boundary">{{ t('monitor.inspector.boundary') }}</p>

    <QueryFeedback
      v-if="optionsPending"
      state="loading"
      :message="t('monitor.inspector.options.loading')"
    />
    <QueryFeedback
      v-else-if="optionsFailed"
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
        id="inspector-protocol"
        :label="t('monitor.inspector.form.protocol')"
        :error="error('protocol')"
      >
        <template #default="{ describedBy }">
          <AppSelect
            id="inspector-protocol"
            :model-value="protocol"
            :label="t('monitor.inspector.form.protocol')"
            :options="protocolOptions"
            :aria-describedby="describedBy"
            :aria-invalid="error('protocol') ? 'true' : undefined"
            @update:model-value="emit('update:protocol', $event)"
          />
        </template>
      </FormField>

      <FormField
        id="inspector-model"
        :label="t('monitor.inspector.form.model')"
        :description="t('monitor.inspector.form.modelOptional')"
        :error="error('externalModel')"
      >
        <template #default="{ describedBy }">
          <input
            id="inspector-model"
            :value="model"
            type="text"
            autocomplete="off"
            :aria-describedby="describedBy"
            :aria-invalid="error('externalModel') ? 'true' : undefined"
            @input="emit('update:model', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>

      <FormField
        id="inspector-access-key"
        :label="t('monitor.inspector.form.accessKey')"
        :error="error('accessKey')"
      >
        <template #default="{ describedBy }">
          <AppSelect
            id="inspector-access-key"
            :model-value="accessKeyId"
            :label="t('monitor.inspector.form.accessKey')"
            :options="accessKeyOptions"
            :aria-describedby="describedBy"
            :aria-invalid="error('accessKey') ? 'true' : undefined"
            @update:model-value="emit('update:accessKeyId', $event)"
          />
        </template>
      </FormField>

      <AppButton type="submit" :disabled="optionsPending">
        {{ t('monitor.inspector.form.submit') }}
      </AppButton>
    </form>

    <p v-if="missingAccessKey" class="inspector-inline-error" role="alert">
      {{ t('monitor.inspector.errors.missingDeepLinkAccessKey', { id: accessKeyId }) }}
    </p>
    <p v-if="hasValidationError" class="sr-only" role="alert">
      {{ t('monitor.inspector.errors.summary') }}
    </p>
  </SurfaceCard>
</template>

<style scoped>
.inspector-form-card {
  display: grid;
  min-width: 0;
  gap: var(--space-5);
}
.inspector-heading {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-4);
}
.inspector-heading h2 {
  margin: 0;
}
.inspector-heading p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}
.inspector-boundary,
.inspector-inline-error {
  margin: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: var(--space-3);
}
.inspector-inline-error {
  border-color: var(--color-danger);
  background: var(--color-danger-bg);
  color: var(--color-text);
}
.inspector-form {
  display: grid;
  grid-template-columns: minmax(150px, 0.8fr) minmax(220px, 1.4fr) minmax(220px, 1.2fr) auto;
  align-items: end;
  gap: var(--space-3);
}
.inspector-form > * {
  min-width: 0;
}
.inspector-form :deep(.app-select__trigger) {
  width: 100%;
}
.inspector-form input {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: 8px 10px;
  font: inherit;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
@media (max-width: 960px) {
  .inspector-form {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 640px) {
  .inspector-form {
    grid-template-columns: minmax(0, 1fr);
  }
  .inspector-heading {
    flex-direction: column;
  }
}
</style>
