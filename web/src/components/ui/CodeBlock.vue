<script setup lang="ts">
import CopyButton from './CopyButton.vue'

withDefaults(
  defineProps<{
    code: string
    language: string
    copyLabel?: string
    copySuccessLabel?: string
    copyFailureLabel?: string
    appearance?: 'default' | 'snippet'
  }>(),
  {
    copyLabel: undefined,
    copySuccessLabel: undefined,
    copyFailureLabel: undefined,
    appearance: 'default',
  },
)
</script>

<template>
  <div class="code-block" :class="`code-block--${appearance}`">
    <div class="code-block__toolbar">
      <span data-code-language>{{ language }}</span>
      <slot name="action">
        <CopyButton
          v-if="copyLabel && copySuccessLabel && copyFailureLabel"
          :value="code"
          :label="copyLabel"
          :success-label="copySuccessLabel"
          :failure-label="copyFailureLabel"
        />
      </slot>
    </div>
    <pre><code>{{ code }}</code></pre>
  </div>
</template>

<style scoped>
.code-block {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-code-bg);
}

.code-block__toolbar {
  display: flex;
  min-height: var(--touch-target);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  color: var(--color-text-muted);
  padding: 0 var(--space-2) 0 var(--space-3);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.code-block pre {
  max-width: 100%;
  margin: 0;
  overflow: auto;
  padding: var(--space-4);
  white-space: pre-wrap;
}

.code-block code {
  color: var(--color-code);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.code-block--snippet {
  overflow: visible;
  border: 0;
  border-radius: 0;
  background: transparent;
}

.code-block--snippet .code-block__toolbar {
  min-height: 0;
  margin-bottom: 6px;
  border: 0;
  padding: 0;
  color: var(--color-text-faint);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
}

.code-block--snippet .code-block__toolbar :deep(.copy-action) {
  width: 28px;
  min-width: 28px;
  height: 24px;
  border: 0;
  background: transparent;
}

.code-block--snippet .code-block__toolbar :deep(.copy-action svg) {
  width: 14px;
  height: 14px;
}

.code-block--snippet pre {
  margin: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: 8px;
  background: var(--color-surface-sunken);
  padding: 11px 13px;
  line-height: 1.7;
  white-space: pre;
}

.code-block--snippet code {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  overflow-wrap: normal;
}
</style>
