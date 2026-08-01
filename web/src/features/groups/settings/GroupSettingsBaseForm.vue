<script setup lang="ts">
import type { GroupProtocol } from '@/api/control/types'
import { enabledDataProtocols } from '@/api/control/protocols'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  section: 'general' | 'routing'
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
const weights = Array.from({ length: 100 }, (_, index) => index + 1)
</script>

<template>
  <section v-if="section === 'general'" id="settings-general" class="group-settings__section">
    <header>
      <h3>{{ t('group.settings.sections.general') }}</h3>
      <p>{{ t('group.settings.base.description') }}</p>
    </header>
    <div class="group-settings__grid">
      <label
        ><span>{{ t('group.settings.base.name') }}</span
        ><input
          :value="name"
          :disabled="pending"
          :aria-invalid="nameError ? 'true' : undefined"
          @input="emit('update:name', ($event.target as HTMLInputElement).value)"
        /><small v-if="nameError" role="alert">{{ nameError }}</small></label
      >
      <label
        ><span>{{ t('group.settings.base.validationModel') }}</span
        ><input
          class="group-settings__mono"
          :value="validationModel ?? ''"
          :disabled="pending"
          @input="
            emit('update:validationModel', ($event.target as HTMLInputElement).value || null)
          "
      /></label>
      <label class="group-settings__wide"
        ><span>{{ t('group.settings.base.upstreamUrl') }}</span
        ><input
          class="group-settings__mono"
          type="url"
          :value="upstreamUrl"
          :disabled="pending"
          :aria-invalid="upstreamUrlError ? 'true' : undefined"
          @input="emit('update:upstreamUrl', ($event.target as HTMLInputElement).value)"
        /><small v-if="upstreamUrlError" role="alert">{{ upstreamUrlError }}</small
        ><small>{{ t('group.settings.base.urlWarning') }}</small></label
      >
    </div>
    <label class="group-settings__switch"
      ><span
        ><strong>{{ t('group.settings.base.enabled') }}</strong
        ><small>{{ t('group.settings.base.enabledHelp') }}</small></span
      ><input
        type="checkbox"
        :checked="enabled"
        :disabled="pending"
        @change="emit('update:enabled', ($event.target as HTMLInputElement).checked)"
    /></label>
  </section>
  <section v-else id="settings-routing" class="group-settings__section">
    <header>
      <h3>{{ t('group.settings.sections.routing') }}</h3>
      <p>{{ t('group.settings.routing.description') }}</p>
    </header>
    <fieldset>
      <legend>{{ t('group.settings.base.protocols') }}</legend>
      <div class="group-settings__checks">
        <label v-for="protocol in enabledDataProtocols" :key="protocol"
          ><input
            type="checkbox"
            :checked="protocols.includes(protocol)"
            :disabled="pending"
            @change="emit('toggleProtocol', protocol, ($event.target as HTMLInputElement).checked)"
          /><code>{{ protocol }}</code></label
        >
      </div>
      <small v-if="protocolsError" role="alert">{{ protocolsError }}</small>
    </fieldset>
    <label
      ><span>{{ t('group.settings.base.weight') }}</span
      ><select
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
        <option v-for="weight in weights" :key="weight" :value="weight">
          {{ weight }}
        </option></select
      ><small>{{ t('group.settings.routing.weightHelp') }}</small></label
    >
  </section>
</template>

<style scoped>
.group-settings__section {
  display: grid;
  gap: var(--space-4);
}
.group-settings__section header h3,
.group-settings__section header p {
  margin: 0;
}
.group-settings__section header p,
small {
  color: var(--color-text-muted);
}
.group-settings__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}
.group-settings__wide {
  grid-column: 1 / -1;
}
.group-settings__grid label,
fieldset,
.group-settings__section > label {
  display: grid;
  gap: var(--space-2);
}
.group-settings__grid label > span,
legend,
.group-settings__section > label > span {
  font-weight: 650;
}
input:not([type='checkbox']),
select {
  width: 100%;
  min-height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: 0 var(--space-3);
  font: inherit;
}
.group-settings__mono,
code {
  font-family: var(--font-mono);
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
.group-settings__switch {
  display: flex !important;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
}
.group-settings__switch {
  justify-content: space-between;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}
.group-settings__switch span {
  display: grid;
}
.group-settings__section small[role='alert'] {
  color: var(--color-danger);
}
@media (max-width: 759px) {
  .group-settings__grid {
    grid-template-columns: 1fr;
  }
  .group-settings__wide {
    grid-column: auto;
  }
}
</style>
