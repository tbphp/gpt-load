<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { AccessKeyOptionDto } from '@/api/control/types'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { ChannelDto } from '@/app/resources/channels'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'

import {
  requestLogCostStates,
  requestLogFailureCategories,
  requestLogPricingCompleteness,
  requestLogRetryStates,
  requestLogUsageStates,
  type LogFilterDraft,
  type LogFilterErrors,
} from './log-filters'

const props = defineProps<{
  open: boolean
  draft: LogFilterDraft
  errors: LogFilterErrors
  accessKeys: AccessKeyOptionDto[]
  channels: ChannelDto[]
  accessKeysFailed: boolean
  channelsFailed: boolean
  selfScoped?: boolean
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  updateField: [field: keyof LogFilterDraft, value: string]
  apply: []
  reset: []
}>()
const { t } = useI18n()

const option = (value: string, label: string) => ({ value, label })
const booleanOptions = () => [
  option('', t('monitor.logs.filters.any')),
  option('true', t('monitor.logs.yes')),
  option('false', t('monitor.logs.no')),
]
const protocolOptions = () => [
  option('', t('monitor.logs.filters.anyProtocol')),
  ...enabledDataProtocols.map((value) => option(value, value)),
]
const usageOptions = () => [
  option('', t('monitor.logs.filters.any')),
  ...requestLogUsageStates.map((value) =>
    option(value, t(`monitor.logs.filters.usageState.${value}`)),
  ),
]
const costOptions = () => [
  option('', t('monitor.logs.filters.any')),
  ...requestLogCostStates.map((value) =>
    option(value, t(`monitor.logs.filters.costState.${value}`)),
  ),
]
const completenessOptions = () => [
  option('', t('monitor.logs.filters.any')),
  ...requestLogPricingCompleteness.map((value) =>
    option(value, t(`monitor.logs.filters.completeness.${value}`)),
  ),
]
const failureOptions = () => [
  option('', t('monitor.logs.filters.any')),
  ...requestLogFailureCategories.map((value) =>
    option(value, t(`monitor.logs.failureCategory.${value}`)),
  ),
]
const retryOptions = () => [
  option('', t('monitor.logs.filters.any')),
  ...requestLogRetryStates.map((value) =>
    option(value, t(`monitor.logs.filters.retryState.${value}`)),
  ),
]
const accessKeyOptions = () => [
  option('', t('monitor.logs.filters.anyAccessKey')),
  ...props.accessKeys.map((key) => option(String(key.id), `${key.name} · #${key.id}`)),
]
const channelOptions = () => {
  const options = [option('', t('monitor.logs.filters.anyChannel'))]
  if (
    props.draft.channel_id &&
    !props.channels.some((channel) => channel.channel_id === props.draft.channel_id)
  ) {
    options.push(option(props.draft.channel_id, props.draft.channel_id))
  }
  return [...options, ...props.channels.map((channel) => option(channel.channel_id, channel.name))]
}

function error(field: keyof LogFilterDraft): string | undefined {
  const key = props.errors[field]
  return key ? t(key) : undefined
}

function update(field: keyof LogFilterDraft, value: string): void {
  emit('updateField', field, value)
}
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="t('monitor.logs.filters.advancedTitle')"
    :description="t('monitor.logs.filters.advancedDescription')"
    :close-label="t('monitor.logs.filters.closeAdvanced')"
    @update:open="emit('update:open', $event)"
  >
    <form class="logs-advanced" @submit.prevent="emit('apply')">
      <section class="logs-advanced__section">
        <h3>{{ t('monitor.logs.filters.sections.request') }}</h3>
        <div class="logs-advanced__grid">
          <FormField
            v-if="!selfScoped"
            id="logs-access-key"
            :label="t('monitor.logs.filters.accessKey')"
            size="compact"
            :error="error('access_key_id')"
          >
            <template #default="{ describedBy, invalid }">
              <AppSelect
                id="logs-access-key"
                :model-value="draft.access_key_id"
                :label="t('monitor.logs.filters.accessKey')"
                :options="accessKeyOptions()"
                size="compact"
                :disabled="accessKeysFailed"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @update:model-value="update('access_key_id', $event)"
              />
            </template>
          </FormField>
          <FormField id="logs-protocol" :label="t('monitor.logs.filters.protocol')" size="compact">
            <AppSelect
              id="logs-protocol"
              :model-value="draft.protocol"
              :label="t('monitor.logs.filters.protocol')"
              :options="protocolOptions()"
              size="compact"
              @update:model-value="update('protocol', $event)"
            />
          </FormField>
          <FormField
            id="logs-request-id"
            :label="t('monitor.logs.filters.requestId')"
            size="compact"
            :error="error('request_id')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                id="logs-request-id"
                :value="draft.request_id"
                class="logs-advanced__mono"
                autocomplete="off"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('request_id', ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
          <FormField id="logs-stream" :label="t('monitor.logs.filters.stream')" size="compact">
            <AppSelect
              id="logs-stream"
              :model-value="draft.stream"
              :label="t('monitor.logs.filters.stream')"
              :options="booleanOptions()"
              size="compact"
              @update:model-value="update('stream', $event)"
            />
          </FormField>
        </div>
      </section>

      <section v-if="!selfScoped" class="logs-advanced__section">
        <h3>{{ t('monitor.logs.filters.sections.attempt') }}</h3>
        <div class="logs-advanced__grid">
          <FormField
            id="logs-channel"
            :label="t('monitor.logs.filters.channel')"
            size="compact"
            :error="error('channel_id')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                v-if="channelsFailed"
                id="logs-channel"
                :value="draft.channel_id"
                autocomplete="off"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('channel_id', ($event.target as HTMLInputElement).value)"
              />
              <AppSelect
                v-else
                id="logs-channel"
                :model-value="draft.channel_id"
                :label="t('monitor.logs.filters.channel')"
                :options="channelOptions()"
                size="compact"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @update:model-value="update('channel_id', $event)"
              />
            </template>
          </FormField>
          <FormField
            id="logs-credential"
            :label="t('monitor.logs.filters.credential')"
            size="compact"
            :error="error('credential_id')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                id="logs-credential"
                :value="draft.credential_id"
                inputmode="numeric"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('credential_id', ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
          <FormField
            id="logs-upstream-model"
            :label="t('monitor.logs.filters.upstreamModel')"
            size="compact"
            :error="error('upstream_model')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                id="logs-upstream-model"
                :value="draft.upstream_model"
                autocomplete="off"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('upstream_model', ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
          <FormField
            id="logs-retry-state"
            :label="t('monitor.logs.filters.retryStateLabel')"
            size="compact"
          >
            <AppSelect
              id="logs-retry-state"
              :model-value="draft.retry_state"
              :label="t('monitor.logs.filters.retryStateLabel')"
              :options="retryOptions()"
              size="compact"
              @update:model-value="update('retry_state', $event)"
            />
          </FormField>
          <FormField
            id="logs-retry-min"
            :label="t('monitor.logs.filters.retryMin')"
            size="compact"
            :error="error('retry_count_min')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                id="logs-retry-min"
                :value="draft.retry_count_min"
                inputmode="numeric"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('retry_count_min', ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
          <FormField
            id="logs-retry-max"
            :label="t('monitor.logs.filters.retryMax')"
            size="compact"
            :error="error('retry_count_max')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                id="logs-retry-max"
                :value="draft.retry_count_max"
                inputmode="numeric"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('retry_count_max', ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
          <FormField
            id="logs-attempt-code"
            :label="t('monitor.logs.filters.attemptStatusCode')"
            size="compact"
            :error="error('attempt_status_code')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                id="logs-attempt-code"
                :value="draft.attempt_status_code"
                inputmode="numeric"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('attempt_status_code', ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
          <FormField
            id="logs-failure"
            :label="t('monitor.logs.filters.failureCategory')"
            size="compact"
          >
            <AppSelect
              id="logs-failure"
              :model-value="draft.failure_category"
              :label="t('monitor.logs.filters.failureCategory')"
              :options="failureOptions()"
              size="compact"
              @update:model-value="update('failure_category', $event)"
            />
          </FormField>
          <FormField
            id="logs-error-code"
            :label="t('monitor.logs.filters.errorCode')"
            size="compact"
            :error="error('error_code')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                id="logs-error-code"
                :value="draft.error_code"
                autocomplete="off"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('error_code', ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
        </div>
      </section>

      <section class="logs-advanced__section">
        <h3>{{ t('monitor.logs.filters.sections.result') }}</h3>
        <div class="logs-advanced__grid">
          <FormField
            id="logs-final-code"
            :label="t('monitor.logs.filters.finalStatusCode')"
            size="compact"
            :error="error('final_status_code')"
          >
            <template #default="{ describedBy, invalid }">
              <input
                id="logs-final-code"
                :value="draft.final_status_code"
                inputmode="numeric"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update('final_status_code', ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
          <FormField
            id="logs-usage-state"
            :label="t('monitor.logs.filters.usageStateLabel')"
            size="compact"
          >
            <AppSelect
              id="logs-usage-state"
              :model-value="draft.usage_state"
              :label="t('monitor.logs.filters.usageStateLabel')"
              :options="usageOptions()"
              size="compact"
              @update:model-value="update('usage_state', $event)"
            />
          </FormField>
          <FormField
            id="logs-cost-state"
            :label="t('monitor.logs.filters.costStateLabel')"
            size="compact"
          >
            <AppSelect
              id="logs-cost-state"
              :model-value="draft.cost_state"
              :label="t('monitor.logs.filters.costStateLabel')"
              :options="costOptions()"
              size="compact"
              @update:model-value="update('cost_state', $event)"
            />
          </FormField>
          <FormField
            id="logs-completeness"
            :label="t('monitor.logs.filters.completenessLabel')"
            size="compact"
          >
            <AppSelect
              id="logs-completeness"
              :model-value="draft.pricing_completeness"
              :label="t('monitor.logs.filters.completenessLabel')"
              :options="completenessOptions()"
              size="compact"
              @update:model-value="update('pricing_completeness', $event)"
            />
          </FormField>
          <FormField id="logs-cache" :label="t('monitor.logs.filters.cachePresent')" size="compact">
            <AppSelect
              id="logs-cache"
              :model-value="draft.cache_present"
              :label="t('monitor.logs.filters.cachePresent')"
              :options="booleanOptions()"
              size="compact"
              @update:model-value="update('cache_present', $event)"
            />
          </FormField>
        </div>
      </section>

      <section class="logs-advanced__section">
        <h3>{{ t('monitor.logs.filters.sections.ranges') }}</h3>
        <div class="logs-advanced__grid">
          <FormField
            v-for="field in [
              'first_response_min_ms',
              'first_response_max_ms',
              'duration_min_ms',
              'duration_max_ms',
              'input_tokens_min',
              'input_tokens_max',
              'output_tokens_min',
              'output_tokens_max',
              'cost_min_usd',
              'cost_max_usd',
            ] as const"
            :id="`logs-${field}`"
            :key="field"
            :label="t(`monitor.logs.filters.rangeFields.${field}`)"
            size="compact"
            :error="error(field)"
          >
            <template #default="{ describedBy, invalid }">
              <input
                :id="`logs-${field}`"
                :value="draft[field]"
                inputmode="decimal"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="update(field, ($event.target as HTMLInputElement).value)"
              />
            </template>
          </FormField>
        </div>
      </section>
    </form>

    <template #footer>
      <AppButton variant="secondary" size="compact" @click="emit('reset')">{{
        t('monitor.logs.filters.reset')
      }}</AppButton>
      <span class="logs-advanced__footer-actions">
        <AppButton variant="secondary" size="compact" @click="emit('update:open', false)">{{
          t('common.cancel')
        }}</AppButton>
        <AppButton size="compact" @click="emit('apply')">{{
          t('monitor.logs.filters.apply')
        }}</AppButton>
      </span>
    </template>
  </AppDrawer>
</template>

<style scoped>
.logs-advanced,
.logs-advanced__section {
  display: grid;
  min-width: 0;
}

.logs-advanced {
  gap: 0;
}

.logs-advanced__section {
  gap: 12px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 16px 0;
}

.logs-advanced__section:last-child {
  border-bottom: 0;
}

.logs-advanced__section h3 {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 650;
}

.logs-advanced__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 10px;
}

.logs-advanced__grid :deep(.app-select__trigger) {
  width: 100%;
}

.logs-advanced__mono {
  font-family: var(--font-mono) !important;
}

.logs-advanced__footer-actions {
  display: flex;
  gap: 8px;
}

@media (max-width: 520px) {
  .logs-advanced__grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .logs-advanced__footer-actions {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
