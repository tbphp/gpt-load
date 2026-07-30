<script setup lang="ts">
import { Search } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { protocolCatalog } from '@/api/control/protocols'
import type { GroupProtocol } from '@/api/control/types'
import type { HeaderRulesDto } from '@/app/resources/groups'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import { channelPresets, type ChannelPreset } from './channel-presets'
import KeyTextarea from './KeyTextarea.vue'

defineProps<{
  presetId: ChannelPreset['id']
  name: string
  upstreamUrl: string
  protocols: GroupProtocol[]
  keys: string
  headerRules: HeaderRulesDto
  pending: boolean
  canDiscover: boolean
}>()
const emit = defineEmits<{
  applyPreset: [id: ChannelPreset['id']]
  'update:name': [value: string]
  'update:upstreamUrl': [value: string]
  toggleProtocol: [protocol: GroupProtocol, checked: boolean]
  'update:keys': [value: string]
  'update:headerRules': [value: HeaderRulesDto]
  headerRulesValid: [valid: boolean]
  discover: []
}>()
const { t } = useI18n()
const heading = ref<HTMLHeadingElement>()
const protocolOptions = protocolCatalog

function focusHeading(): void {
  heading.value?.focus()
}

defineExpose({ focusHeading })
</script>

<template>
  <SurfaceCard class="import-card">
    <header>
      <h2 ref="heading" data-test="import-step-1-heading" tabindex="-1">
        {{ t('import.connection.title') }}
      </h2>
      <p>{{ t('import.connection.description') }}</p>
    </header>
    <div class="connection-grid">
      <label>
        <span>{{ t('import.connection.preset') }}</span>
        <select
          data-test="preset"
          :value="presetId"
          @change="
            emit('applyPreset', ($event.target as HTMLSelectElement).value as ChannelPreset['id'])
          "
        >
          <option v-for="preset in channelPresets" :key="preset.id" :value="preset.id">
            {{ t(preset.labelKey) }}
          </option>
        </select>
      </label>
      <label>
        <span>{{ t('import.connection.name') }}</span>
        <input
          :value="name"
          data-test="group-name"
          autocomplete="off"
          @input="emit('update:name', ($event.target as HTMLInputElement).value)"
        />
      </label>
      <label class="wide">
        <span>{{ t('import.connection.url') }}</span>
        <input
          :value="upstreamUrl"
          data-test="upstream-url"
          type="url"
          autocomplete="off"
          spellcheck="false"
          @input="emit('update:upstreamUrl', ($event.target as HTMLInputElement).value)"
        />
      </label>
    </div>
    <fieldset>
      <legend>{{ t('import.connection.protocols') }}</legend>
      <label v-for="protocol in protocolOptions" :key="protocol.value" class="protocol-option">
        <input
          :data-test="`protocol-${protocol.value}`"
          type="checkbox"
          :checked="protocols.includes(protocol.value)"
          @change="
            emit('toggleProtocol', protocol.value, ($event.target as HTMLInputElement).checked)
          "
        />{{ t(protocol.labelKey) }}
      </label>
    </fieldset>
    <InlineFeedback
      v-if="protocols.includes('openai-responses')"
      data-test="import-responses-affinity-warning"
      tone="warning"
    >
      {{ t('import.connection.responsesAffinityWarning') }}
    </InlineFeedback>
    <KeyTextarea
      :model-value="keys"
      :disabled="pending"
      @update:model-value="emit('update:keys', $event)"
    />
    <details
      class="advanced"
      :open="Object.keys(headerRules.set).length > 0 || headerRules.remove.length > 0"
    >
      <summary>{{ t('import.connection.advanced') }}</summary>
      <HeaderRulesEditor
        :model-value="headerRules"
        @update:model-value="emit('update:headerRules', $event)"
        @update:valid="emit('headerRulesValid', $event)"
      />
    </details>
    <footer class="card-actions">
      <AppButton
        data-test="discover"
        :disabled="!canDiscover"
        :busy="pending"
        @click="emit('discover')"
      >
        <Search :size="16" aria-hidden="true" />{{ t('import.discover') }}
      </AppButton>
    </footer>
  </SurfaceCard>
</template>

<style scoped>
.import-card {
  display: grid;
  gap: var(--space-5);
  padding: var(--space-6);
}
header h2,
header p {
  margin: 0;
}
header h2 {
  font-size: 1.2rem;
}
header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.connection-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-4);
}
.connection-grid label {
  display: grid;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-size: 0.75rem;
  font-weight: 650;
}
.connection-grid .wide {
  grid-column: 1 / -1;
}
.connection-grid input,
.connection-grid select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
}
.wide input {
  font-family: ui-monospace, monospace;
}
fieldset {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  border: 0;
  margin: 0;
  padding: 0;
}
legend {
  width: 100%;
  margin-bottom: var(--space-2);
  color: var(--color-text-muted);
  font-size: 0.75rem;
  font-weight: 650;
}
.protocol-option {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  padding: var(--space-2) var(--space-3);
  cursor: pointer;
}
.advanced {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}
.advanced summary {
  min-height: 44px;
  cursor: pointer;
  color: var(--color-text-muted);
  font-weight: 650;
}
.card-actions {
  display: flex;
  justify-content: flex-end;
}
.card-actions :deep(.app-button) {
  gap: var(--space-2);
}
@media (max-width: 640px) {
  .connection-grid {
    grid-template-columns: 1fr;
  }
  .connection-grid .wide {
    grid-column: auto;
  }
}
</style>
