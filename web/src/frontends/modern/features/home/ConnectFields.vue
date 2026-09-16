<script setup lang="ts">
import { Copy } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { AppCopyValue, AppIconButton, AppOverflowText } from '@modern/components/ui'
import type { GatewayField } from './gateway-config'

defineProps<{
  fields: GatewayField[]
  copyable: boolean
  resolveKey: () => Promise<string>
}>()
const { t } = useI18n()
</script>

<template>
  <dl class="modern-connect-fields">
    <div v-for="field in fields" :key="field.slot" class="modern-connect-field">
      <dt>{{ t('home.slots.' + field.slot) }}</dt>
      <dd>
        <AppOverflowText :text="field.value || t('home.modelPending')" />
        <AppCopyValue
          v-if="copyable && field.value"
          :value="field.value"
          :resolve-value="field.slot === 'apiKey' ? resolveKey : undefined"
        >
          <template #trigger="{ copy, pending }">
            <AppIconButton
              :icon="Copy"
              :label="t('home.copyField', { field: t('home.slots.' + field.slot) })"
              :loading="pending"
              size="xxs"
              @click="copy()"
            />
          </template>
        </AppCopyValue>
      </dd>
    </div>
  </dl>
</template>

<style scoped>
.modern-connect-fields {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
  margin: 0;
}
.modern-connect-field {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-3);
  min-width: 0;
}
.modern-connect-field dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-connect-field dd {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  min-width: 0;
  min-height: var(--modern-control-sm);
  margin: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: var(--modern-space-1) var(--modern-space-1) var(--modern-space-1) var(--modern-space-3);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
}
</style>
