<script setup lang="ts">
import { Copy } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppCopyValue, AppOverflowText } from '@modern/components/ui'
import type { ConfigBlock } from './gateway-config'

defineProps<{
  blocks: (ConfigBlock & { resolve: () => Promise<string> })[]
  copyable: boolean
}>()
const { t, n } = useI18n()
</script>

<template>
  <ol class="modern-connect-steps">
    <li v-for="(block, index) in blocks" :key="block.label" class="modern-connect-step">
      <div class="modern-connect-step-heading">
        <span class="modern-connect-step-number" aria-hidden="true">{{ n(index + 1) }}</span>
        <h3>
          <template v-if="block.label === 'shell'">{{ t('home.runInTerminal') }}</template>
          <template v-else>
            <span>{{ t('home.writeConfig') }}</span>
            <AppOverflowText class="modern-connect-step-path" :text="block.label" />
          </template>
        </h3>
        <AppCopyValue :value="block.content" :resolve-value="block.resolve">
          <template #trigger="{ copy, pending }">
            <AppButton
              size="xs"
              :icon="Copy"
              :loading="pending"
              :disabled="!copyable"
              :aria-label="t('home.copyStep', { step: n(index + 1) })"
              @click="copy()"
              >{{ t('ui.copy.action') }}</AppButton
            >
          </template>
        </AppCopyValue>
      </div>
      <pre
        tabindex="0"
        :aria-label="block.label === 'shell' ? t('home.terminal') : block.label"
      ><code>{{ block.content }}</code></pre>
    </li>
  </ol>
</template>

<style scoped>
.modern-connect-steps {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-connect-step {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-connect-step-heading {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-connect-step-number {
  display: grid;
  width: var(--modern-space-5);
  height: var(--modern-space-5);
  flex: none;
  place-items: center;
  border-radius: var(--modern-radius-round);
  background: var(--modern-subtle);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-connect-step-heading h3 {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1-5);
  min-width: 0;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-connect-step-path {
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  padding: var(--modern-space-0-5) var(--modern-space-1);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
}
.modern-connect-step pre {
  overflow-x: auto;
  min-width: 0;
  margin: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: var(--modern-space-3);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  scrollbar-gutter: var(--modern-scrollbar-gutter);
}
.modern-connect-step code {
  font: inherit;
}
</style>
