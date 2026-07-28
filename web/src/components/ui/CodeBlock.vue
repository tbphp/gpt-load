<script setup lang="ts">
import CopyButton from './CopyButton.vue'

defineProps<{
  code: string
  language: string
  copyLabel: string
  copySuccessLabel: string
  copyFailureLabel: string
}>()
</script>

<template>
  <div class="code-block">
    <div class="code-block__toolbar">
      <span data-code-language>{{ language }}</span>
      <CopyButton
        :value="code"
        :label="copyLabel"
        :success-label="copySuccessLabel"
        :failure-label="copyFailureLabel"
      />
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
</style>
