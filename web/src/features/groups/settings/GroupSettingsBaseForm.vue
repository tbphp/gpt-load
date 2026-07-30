<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { enabledDataProtocols } from '@/api/control/protocols'
import type { GroupProtocol } from '@/api/control/types'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

defineProps<{
  name: string
  upstreamUrl: string
  validationModel: string | null
  weightManual: number | null
  protocols: GroupProtocol[]
  enabled: boolean
  pending: boolean
  nameError: string
  upstreamUrlError: string
  protocolsError: string
}>()
const emit = defineEmits<{
  'update:name': [value: string]
  'update:upstreamUrl': [value: string]
  'update:validationModel': [value: string | null]
  'update:weightManual': [value: number | null]
  toggleProtocol: [protocol: GroupProtocol, checked: boolean]
  'update:enabled': [value: boolean]
}>()
const { t } = useI18n()
const protocolOptions: readonly GroupProtocol[] = enabledDataProtocols
const weights = Array.from({ length: 100 }, (_, index) => index + 1)
</script>

<template>
  <SurfaceCard class="group-settings__card">
    <div class="group-settings__section-heading">
      <h3>{{ t('group.settings.base.title') }}</h3>
      <p>{{ t('group.settings.base.description') }}</p>
    </div>
    <div class="group-settings__grid">
      <label>
        <span>{{ t('group.settings.base.name') }}</span>
        <input
          :value="name"
          data-test="group-name"
          type="text"
          autocomplete="off"
          :aria-invalid="nameError ? 'true' : undefined"
          :aria-describedby="nameError ? 'group-name-error' : undefined"
          :disabled="pending"
          @input="emit('update:name', ($event.target as HTMLInputElement).value)"
        />
        <small
          v-if="nameError"
          id="group-name-error"
          data-test="group-name-error"
          class="group-settings__field-error"
          role="alert"
        >
          {{ nameError }}
        </small>
      </label>
      <label>
        <span>{{ t('group.settings.base.upstreamUrl') }}</span>
        <input
          :value="upstreamUrl"
          data-test="group-upstream-url"
          class="group-settings__mono"
          type="url"
          autocomplete="off"
          :aria-invalid="upstreamUrlError ? 'true' : undefined"
          :aria-describedby="
            upstreamUrlError ? 'group-upstream-url-error group-upstream-url-warning' : undefined
          "
          :disabled="pending"
          @input="emit('update:upstreamUrl', ($event.target as HTMLInputElement).value)"
        />
        <small
          v-if="upstreamUrlError"
          id="group-upstream-url-error"
          data-test="group-upstream-url-error"
          class="group-settings__field-error"
          role="alert"
        >
          {{ upstreamUrlError }}
        </small>
        <small id="group-upstream-url-warning">{{ t('group.settings.base.urlWarning') }}</small>
      </label>
      <label>
        <span>{{ t('group.settings.base.validationModel') }}</span>
        <input
          data-test="group-validation-model"
          class="group-settings__mono"
          type="text"
          autocomplete="off"
          :value="validationModel ?? ''"
          :disabled="pending"
          @input="emit('update:validationModel', ($event.target as HTMLInputElement).value || null)"
        />
      </label>
      <label>
        <span>{{ t('group.settings.base.weight') }}</span>
        <select
          data-test="group-weight"
          :value="weightManual ?? 'auto'"
          :disabled="pending"
          @change="
            emit(
              'update:weightManual',
              ($event.target as HTMLSelectElement).value === 'auto'
                ? null
                : Number(($event.target as HTMLSelectElement).value),
            )
          "
        >
          <option value="auto">{{ t('group.settings.base.auto') }}</option>
          <option v-if="weightManual === 0" value="0" disabled>0</option>
          <option v-for="weight in weights" :key="weight" :value="weight">{{ weight }}</option>
        </select>
      </label>
    </div>
    <fieldset
      :aria-invalid="protocolsError ? 'true' : undefined"
      :aria-describedby="protocolsError ? 'group-protocols-error' : undefined"
    >
      <legend>{{ t('group.settings.base.protocols') }}</legend>
      <div class="group-settings__checks">
        <label v-for="protocol in protocolOptions" :key="protocol">
          <input
            :data-test="`group-protocol-${protocol}`"
            type="checkbox"
            :checked="protocols.includes(protocol)"
            :disabled="pending"
            @change="emit('toggleProtocol', protocol, ($event.target as HTMLInputElement).checked)"
          />
          {{ t(`common.protocols.${protocol}`) }}
        </label>
      </div>
      <small
        v-if="protocolsError"
        id="group-protocols-error"
        data-test="group-protocols-error"
        class="group-settings__field-error"
        role="alert"
      >
        {{ protocolsError }}
      </small>
      <InlineFeedback
        v-if="protocols.includes('openai-responses')"
        data-test="group-responses-affinity-warning"
        tone="warning"
      >
        {{ t('group.settings.base.responsesAffinityWarning') }}
      </InlineFeedback>
      <small
        v-if="protocols.includes('openai-responses')"
        data-test="group-responses-usage-options-help"
      >
        {{ t('group.settings.base.responsesUsageOptionsHelp') }}
      </small>
    </fieldset>
    <label class="group-settings__enabled">
      <input
        :checked="enabled"
        data-test="group-enabled"
        type="checkbox"
        :disabled="pending"
        @change="emit('update:enabled', ($event.target as HTMLInputElement).checked)"
      />
      <span>{{ t('group.settings.base.enabled') }}</span>
    </label>
  </SurfaceCard>
</template>

<style scoped>
.group-settings__card,
.group-settings__section-heading {
  display: grid;
  gap: var(--space-3);
}
.group-settings__section-heading {
  gap: 0;
}
.group-settings__section-heading h3,
.group-settings__section-heading p {
  margin: 0;
}
.group-settings__section-heading p,
small {
  color: var(--color-text-muted);
}
.group-settings__section-heading p {
  margin-top: var(--space-1);
}
.group-settings__field-error {
  color: var(--color-danger);
  font-size: 0.8125rem;
}
.group-settings__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}
.group-settings__grid label,
fieldset {
  display: grid;
  gap: var(--space-2);
}
.group-settings__grid label > span,
legend {
  font-weight: 650;
}
input[type='text'],
input[type='url'],
select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
.group-settings__mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
fieldset {
  margin: 0;
  border: 0;
  padding: 0;
}
.group-settings__checks {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-4);
}
.group-settings__checks label,
.group-settings__enabled {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}
input[type='checkbox'] {
  width: 18px;
  height: 18px;
}
@media (max-width: 760px) {
  .group-settings__grid {
    grid-template-columns: 1fr;
  }
}
</style>
