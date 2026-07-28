<script setup lang="ts">
import { ChevronDown, ChevronUp, KeyRound, Terminal, TriangleAlert } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyOptionDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import CodeBlock from '@/components/ui/CodeBlock.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import {
  buildConnectionSnippet,
  isLoopbackHostname,
  selectInitialAccessKey,
  type NativeProtocol,
} from './home-model'
import {
  createConnectionPreference,
  resolveConnectionPreferenceStorage,
} from './connection-preference'

const props = withDefaults(
  defineProps<{
    keys: AccessKeyOptionDto[]
    modelIds: string[]
    origin?: string
  }>(),
  { origin: () => window.location.origin },
)
const { t } = useI18n()
const modelPlaceholder = '<MODEL_ID>'
const selectedId = ref<number>()
const selectedProtocol = ref<NativeProtocol>('openai')
const selectedModel = ref('')
const preference = createConnectionPreference(resolveConnectionPreferenceStorage())
const expanded = ref(preference.initialExpanded)
watch(
  () => props.keys,
  (keys) => {
    if (!keys.some((key) => key.id === selectedId.value)) {
      selectedId.value = selectInitialAccessKey(keys)
    }
  },
  { immediate: true },
)
const selectedKey = computed(() => props.keys.find((key) => key.id === selectedId.value))
const keyOptions = computed(() =>
  props.keys.map((key) => ({ value: String(key.id), label: key.name })),
)
const protocolOptions = computed(() => [
  { value: 'openai', label: t('common.protocols.openai') },
  { value: 'anthropic', label: t('common.protocols.anthropic') },
  { value: 'gemini', label: t('common.protocols.gemini') },
])
const hostname = computed(() => {
  try {
    return new URL(props.origin).hostname
  } catch {
    return ''
  }
})
const loopback = computed(() => isLoopbackHostname(hostname.value))
const model = computed(() => selectedModel.value.trim() || modelPlaceholder)
const snippet = computed(() =>
  buildConnectionSnippet({
    origin: props.origin,
    protocol: selectedProtocol.value,
    model: model.value,
  }),
)

function setExpanded(next: boolean): void {
  expanded.value = next
  preference.setExpanded(next)
}
</script>

<template>
  <SurfaceCard class="connection-card">
    <header class="connection-card__header">
      <div class="connection-card__title">
        <Terminal :size="20" aria-hidden="true" />
        <div>
          <h2>{{ t('home.connection') }}</h2>
          <p>{{ t('home.connectionDescription') }}</p>
        </div>
      </div>
      <AppButton
        data-test="connection-toggle"
        variant="ghost"
        size="sm"
        :aria-expanded="expanded"
        @click="setExpanded(!expanded)"
      >
        <ChevronUp v-if="expanded" :size="16" aria-hidden="true" />
        <ChevronDown v-else :size="16" aria-hidden="true" />
        {{ expanded ? t('home.collapseConnection') : t('home.expandConnection') }}
      </AppButton>
    </header>

    <div v-if="expanded" data-test="connection-content" class="connection-card__content">
      <div class="connection-card__field">
        <span class="connection-card__label">{{ t('home.baseUrl') }}</span>
        <div class="connection-card__value">
          <code>{{ origin }}</code>
          <CopyButton
            :value="origin"
            :label="t('home.copyBaseUrl')"
            :success-label="t('common.copied')"
            :failure-label="t('common.copyFailed')"
          />
        </div>
        <p v-if="loopback" class="loopback-note" role="note">
          <TriangleAlert :size="17" aria-hidden="true" />{{ t('home.loopbackWarning') }}
        </p>
      </div>

      <div class="connection-card__field">
        <span class="connection-card__label">{{ t('home.accessKey') }}</span>
        <template v-if="selectedKey">
          <AppSelect
            :model-value="String(selectedId)"
            :label="t('home.accessKey')"
            :options="keyOptions"
            @update:model-value="selectedId = Number($event)"
          />
          <div class="connection-card__value">
            <span>{{ selectedKey.name }}</span>
            <RouterLink class="text-link" to="/access-keys">
              {{ t('home.manageAccessKeys') }}
            </RouterLink>
          </div>
        </template>
        <div v-else class="connection-card__empty">
          <KeyRound :size="18" aria-hidden="true" />
          <span>{{ t('home.noAccessKey') }}</span>
          <RouterLink class="text-link" to="/access-keys">{{
            t('home.createAccessKey')
          }}</RouterLink>
        </div>
      </div>

      <div class="connection-card__choices">
        <label>
          <span class="connection-card__label">{{ t('home.protocol') }}</span>
          <AppSelect
            :model-value="selectedProtocol"
            :label="t('home.protocol')"
            :options="protocolOptions"
            @update:model-value="selectedProtocol = $event as NativeProtocol"
          />
        </label>
        <label>
          <span class="connection-card__label">{{ t('home.model') }}</span>
          <input
            v-model="selectedModel"
            data-test="connection-model"
            list="connection-model-options"
            :placeholder="modelPlaceholder"
          />
          <datalist id="connection-model-options">
            <option v-for="modelId in modelIds" :key="modelId" :value="modelId" />
          </datalist>
        </label>
      </div>

      <div class="connection-card__field">
        <span class="connection-card__label">{{ t('home.snippet') }}</span>
        <p class="connection-card__environment-note">{{ t('home.environmentKeyHint') }}</p>
        <CodeBlock
          data-test="connection-snippet"
          :code="snippet.command"
          :language="snippet.language"
          :copy-label="t('home.copySnippet')"
          :copy-success-label="t('common.copied')"
          :copy-failure-label="t('common.copyFailed')"
        />
        <p v-if="model === modelPlaceholder" class="connection-card__hint">
          {{ t('home.modelPlaceholder', { model: modelPlaceholder }) }}
        </p>
      </div>
    </div>
  </SurfaceCard>
</template>

<style scoped>
.connection-card {
  display: grid;
  gap: var(--space-5);
  padding: var(--space-5);
}
.connection-card__title {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
}
.connection-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.connection-card__header :deep(.app-button) {
  gap: var(--space-1);
}
.connection-card__content {
  display: grid;
  gap: var(--space-5);
}
.connection-card h2 {
  margin: 0;
  font-size: 1.0625rem;
}
.connection-card__title p,
.connection-card__hint {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}
.connection-card__field {
  display: grid;
  gap: var(--space-2);
}
.connection-card__label {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  font-weight: 650;
}
.connection-card__choices {
  display: grid;
  grid-template-columns: minmax(0, 0.8fr) minmax(0, 1.2fr);
  gap: var(--space-3);
}
.connection-card__choices label {
  display: grid;
  gap: var(--space-2);
}
.connection-card__choices :deep(.app-select__trigger),
.connection-card__choices input {
  width: 100%;
}
.connection-card__choices input {
  min-height: var(--touch-target);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
.connection-card__value {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  padding: 4px 4px 4px var(--space-3);
}
.connection-card__value code {
  min-width: 0;
  overflow-wrap: anywhere;
}
.loopback-note,
.connection-card__empty,
.connection-card__environment-note {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  margin: 0;
}
.loopback-note {
  color: var(--color-warning);
  font-size: 0.8125rem;
}
.connection-card__empty {
  flex-wrap: wrap;
  color: var(--color-text-muted);
}
.connection-card__environment-note {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.text-link {
  color: var(--color-primary);
  font-weight: 650;
}
@media (max-width: 480px) {
  .connection-card__header {
    align-items: stretch;
    flex-direction: column;
  }
  .connection-card__choices {
    grid-template-columns: 1fr;
  }
  .connection-card__value {
    align-items: flex-start;
  }
}
</style>
