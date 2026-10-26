<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { priceFields } from '@modern/api/models'
import { AppIconButton, AppTextField } from '@modern/components/ui'
import type { PriceDraftTier } from './model-price-draft'

defineProps<{ pending: boolean; error: (key: string) => string | undefined }>()
const tier = defineModel<PriceDraftTier>('tier', { required: true })
defineEmits<{ remove: [] }>()
const { t } = useI18n()
</script>

<template>
  <div class="modern-price-tier">
    <header>
      <AppTextField
        v-model="tier.threshold"
        :label="t('modelManager.threshold')"
        :error="error(tier.key)"
        :disabled="pending"
        size="sm"
        inputmode="numeric"
      />
      <AppIconButton
        :icon="Trash2"
        :label="t('modelManager.removeTier')"
        size="xs"
        :disabled="pending"
        @click="$emit('remove')"
      />
    </header>
    <div class="modern-price-tier-inputs">
      <AppTextField
        v-for="field in priceFields"
        :key="field"
        v-model="tier.prices[field]"
        :label="t('modelManager.slots.' + field)"
        :error="error(tier.key + '.' + field)"
        :disabled="pending"
        :placeholder="t('modelManager.emptySlot')"
        size="sm"
        inputmode="decimal"
      />
    </div>
    <p v-if="error(tier.key + '.prices')" class="modern-price-tier-error">
      {{ error(tier.key + '.prices') }}
    </p>
  </div>
</template>

<style scoped>
.modern-price-tier {
  display: grid;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-3);
}
.modern-price-tier header {
  display: flex;
  align-items: flex-end;
  gap: var(--modern-space-3);
}
.modern-price-tier header > :first-child {
  flex: 1;
  min-width: 0;
}
.modern-price-tier-inputs {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--modern-space-2);
}
.modern-price-tier-error {
  margin: 0;
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
}
@container price-editor (max-width: 400px) {
  .modern-price-tier-inputs {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
