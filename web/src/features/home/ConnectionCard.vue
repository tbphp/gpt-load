<script setup lang="ts">
import { KeyRound, Terminal, TriangleAlert } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyDto } from '@/api/control/types'
import AppSelect from '@/components/ui/AppSelect.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import SecretValue from '@/components/ui/SecretValue.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import { isLoopbackHostname, selectInitialAccessKey } from './home-model'

const props = withDefaults(
  defineProps<{
    keys: AccessKeyDto[]
    modelIds: string[]
    origin?: string
  }>(),
  { origin: () => window.location.origin },
)
const { t } = useI18n()
const modelPlaceholder = '<MODEL_ID>'
const selectedId = ref<number>()
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
const hostname = computed(() => {
  try {
    return new URL(props.origin).hostname
  } catch {
    return ''
  }
})
const loopback = computed(() => isLoopbackHostname(hostname.value))
const model = computed(() => props.modelIds[0] ?? modelPlaceholder)
const snippet = computed(
  () =>
    `curl "${props.origin}/v1/chat/completions" \\\n  -H "Authorization: Bearer $GPT_LOAD_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"${model.value}"}'`,
)
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
    </header>

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
          <SecretValue
            :value="selectedKey.key"
            :reveal-label="t('common.reveal')"
            :conceal-label="t('common.conceal')"
          />
          <CopyButton
            :value="selectedKey.key"
            :label="t('home.copyAccessKey')"
            :success-label="t('common.copied')"
            :failure-label="t('common.copyFailed')"
          />
        </div>
      </template>
      <div v-else class="connection-card__empty">
        <KeyRound :size="18" aria-hidden="true" />
        <span>{{ t('home.noAccessKey') }}</span>
        <RouterLink class="text-link" to="/access-keys">{{ t('home.createAccessKey') }}</RouterLink>
      </div>
    </div>

    <div class="connection-card__field">
      <span class="connection-card__label">{{ t('home.snippet') }}</span>
      <pre><code>{{ snippet }}</code></pre>
      <p v-if="model === modelPlaceholder" class="connection-card__hint">
        {{ t('home.modelPlaceholder', { model: modelPlaceholder }) }}
      </p>
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
.connection-card__empty {
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
.text-link {
  color: var(--color-primary);
  font-weight: 650;
}
pre {
  max-width: 100%;
  margin: 0;
  overflow-x: auto;
  border-radius: var(--radius-control);
  background: var(--color-code-bg);
  color: var(--color-code);
  padding: var(--space-4);
  line-height: 1.6;
}
@media (max-width: 480px) {
  .connection-card__value {
    align-items: flex-start;
  }
}
</style>
