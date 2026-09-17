<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { AppCopyValue, AppTextField } from '@modern/components/ui'
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
        <AppTextField
          :model-value="field.value"
          :label="t('home.slots.' + field.slot)"
          :placeholder="t('home.modelPending')"
          size="xs"
          label-hidden
          readonly
        >
          <template #suffix>
            <AppCopyValue
              v-if="copyable && field.value"
              :value="field.value"
              display=""
              :label="t('home.copyField', { field: t('home.slots.' + field.slot) })"
              :resolve-value="field.slot === 'apiKey' ? resolveKey : undefined"
            />
          </template>
        </AppTextField>
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
  min-width: 0;
  margin: 0;
  font-family: var(--modern-font-mono);
}
</style>
