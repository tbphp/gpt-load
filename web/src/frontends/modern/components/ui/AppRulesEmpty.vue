<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <div class="modern-rules-empty">
    <slot>{{ t('ui.noRules') }}</slot>
  </div>
</template>

<style scoped>
.modern-rules-empty {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  padding: var(--modern-space-3);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
</style>
