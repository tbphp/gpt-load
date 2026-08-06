<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProviderSuggestion } from '@/app/resources/providers'
import AppButton from '@/components/ui/AppButton.vue'

const props = defineProps<{
  modelValue: string | null
  selectedProvider: ProviderSuggestion | null
  officialProviders: readonly ProviderSuggestion[]
  disabled?: boolean
}>()
const emit = defineEmits<{
  select: [provider: ProviderSuggestion | null]
  browse: []
}>()
const { t } = useI18n()

const currentChannel = computed(() =>
  props.selectedProvider?.source && props.selectedProvider.source !== 'official'
    ? props.selectedProvider
    : null,
)
const customSelected = computed(() => props.modelValue === null)

function choose(provider: ProviderSuggestion | null): void {
  if (props.disabled) return
  emit('select', provider)
}

function providerDescription(provider: ProviderSuggestion): string {
  return provider.protocols.length
    ? provider.protocols.join(' · ')
    : t('import.presets.protocolsUnavailable')
}

function providerSelected(provider: ProviderSuggestion): boolean {
  return props.modelValue === provider.provider_id
}
</script>

<template>
  <section class="preset-picker" aria-labelledby="provider-presets-heading">
    <header class="preset-picker__header">
      <div>
        <h2 id="provider-presets-heading">{{ t('import.presets.title') }}</h2>
        <p>{{ t('import.presets.description') }}</p>
      </div>
      <div class="preset-picker__header-actions">
        <AppButton variant="secondary" size="compact" :disabled="disabled" @click="emit('browse')">
          {{ t('import.presets.browse') }}
        </AppButton>
        <AppButton
          variant="secondary"
          size="compact"
          :disabled="disabled || customSelected"
          :aria-pressed="customSelected"
          @click="choose(null)"
        >
          {{ t('import.presets.custom.name') }}
        </AppButton>
      </div>
    </header>

    <div class="preset-picker__featured">
      <button
        v-for="provider in officialProviders"
        :key="provider.provider_id"
        class="preset-picker__choice"
        :class="{ 'preset-picker__choice--selected': providerSelected(provider) }"
        type="button"
        :disabled="disabled"
        :aria-pressed="providerSelected(provider)"
        @click="choose(provider)"
      >
        <span class="preset-picker__mark">{{ provider.mark }}</span>
        <strong>{{ provider.name }}</strong>
        <span class="preset-picker__description">{{ providerDescription(provider) }}</span>
        <Check
          v-if="providerSelected(provider)"
          class="preset-picker__selected-icon"
          :size="16"
          aria-hidden="true"
        />
      </button>

      <button
        v-if="currentChannel"
        key="current-channel"
        class="preset-picker__choice preset-picker__choice--selected"
        type="button"
        :disabled="disabled"
        aria-pressed="true"
      >
        <span class="preset-picker__mark">{{ currentChannel.mark || '···' }}</span>
        <strong>{{ currentChannel.name }}</strong>
        <span class="preset-picker__description">{{ providerDescription(currentChannel) }}</span>
        <Check class="preset-picker__selected-icon" :size="16" aria-hidden="true" />
      </button>
    </div>
  </section>
</template>

<style scoped>
.preset-picker {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
}

.preset-picker__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.preset-picker__header h2,
.preset-picker__header p {
  margin: 0;
}

.preset-picker__header h2 {
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
}

.preset-picker__header p {
  margin-top: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.preset-picker__header-actions {
  display: flex;
  flex: none;
  gap: var(--space-2);
}

.preset-picker__featured {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
}

.preset-picker__choice {
  position: relative;
  display: grid;
  min-height: 88px;
  align-content: start;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-3);
  text-align: left;
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}

.preset-picker__choice:hover:not(:disabled) {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
}

.preset-picker__choice--selected {
  border-color: var(--color-action);
  box-shadow: inset 3px 0 0 var(--color-action);
  cursor: default;
}

.preset-picker__choice > strong {
  margin-top: 7px;
  font-size: 12.5px;
  font-weight: 600;
}

.preset-picker__description {
  margin-top: 3px;
  color: var(--color-text-faint);
  font-size: 10.8px;
  line-height: var(--line-normal);
}

.preset-picker__choice:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.preset-picker__mark {
  display: grid;
  width: 25px;
  height: 25px;
  place-items: center;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-weight: 700;
}

.preset-picker__selected-icon {
  position: absolute;
  top: 10px;
  right: 10px;
  color: var(--color-action);
}

@media (max-width: 860px) {
  .preset-picker__header {
    flex-direction: column;
  }

  .preset-picker__header-actions {
    align-self: stretch;
  }

  .preset-picker__header-actions :deep(.app-button) {
    flex: 1;
  }

  .preset-picker__featured {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 520px) {
  .preset-picker__featured {
    grid-template-columns: 1fr;
  }
}
</style>
