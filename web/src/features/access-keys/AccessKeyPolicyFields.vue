<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'
import { currentTimeZone } from '@/lib/time'

const props = defineProps<{
  expirationMode: 'never' | 'specified'
  expiresAt: number | null
  baseExpiresAt?: number | null
  sourceMode: 'all' | 'restricted'
  allowedCidrs: string[]
  disabled: boolean
}>()
const emit = defineEmits<{
  'update:expirationMode': [value: 'never' | 'specified']
  'update:expiresAt': [value: number | null]
  'update:sourceMode': [value: 'all' | 'restricted']
  'update:allowedCidrs': [value: string[]]
}>()
const { t } = useI18n()

const expirationOptions = computed(() => [
  { value: 'never', label: t('accessKeys.drawer.expirationNever') },
  { value: 'specified', label: t('accessKeys.drawer.expirationSpecified') },
])
const sourceOptions = computed(() => [
  { value: 'all', label: t('accessKeys.drawer.sourceAll') },
  { value: 'restricted', label: t('accessKeys.drawer.sourceRestricted') },
])

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

function localDateTimeValue(epochMS: number | null): string {
  if (epochMS === null || !Number.isSafeInteger(epochMS) || epochMS <= 0) return ''
  const date = new Date(epochMS)
  if (Number.isNaN(date.getTime())) return ''
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

const expirationInput = computed(() => localDateTimeValue(props.expiresAt))
const minimumExpiration = computed(() => {
  const nextSecond = Date.now() + 1_000
  if (props.expiresAt !== null && props.expiresAt === props.baseExpiresAt) {
    return localDateTimeValue(Math.min(props.expiresAt, nextSecond))
  }
  return localDateTimeValue(nextSecond)
})
const expirationError = computed(() => {
  if (props.expirationMode !== 'specified') return undefined
  if (props.expiresAt === null || props.expiresAt <= 0) {
    return t('accessKeys.drawer.expirationRequired')
  }
  if (props.expiresAt !== props.baseExpiresAt && props.expiresAt <= Date.now()) {
    return t('accessKeys.drawer.expirationFuture')
  }
  return undefined
})
const cidrInput = computed(() => props.allowedCidrs.join('\n'))
const normalizedCIDRCount = computed(
  () => new Set(props.allowedCidrs.map((value) => value.trim()).filter(Boolean)).size,
)
const cidrError = computed(() => {
  if (props.sourceMode !== 'restricted') return undefined
  if (normalizedCIDRCount.value === 0) return t('accessKeys.drawer.sourceRequired')
  if (normalizedCIDRCount.value > 64) return t('accessKeys.drawer.sourceLimit')
  return undefined
})

function setExpirationMode(value: string): void {
  if (value !== 'never' && value !== 'specified') return
  emit('update:expirationMode', value)
  if (value === 'never') emit('update:expiresAt', null)
}

function setExpiration(value: string): void {
  if (value === '') {
    emit('update:expiresAt', 0)
    return
  }
  const epochMS = new Date(value).getTime()
  emit('update:expiresAt', Number.isSafeInteger(epochMS) ? epochMS : 0)
}

function setSourceMode(value: string): void {
  if (value === 'all' || value === 'restricted') emit('update:sourceMode', value)
}

function setCIDRs(value: string): void {
  emit('update:allowedCidrs', value.split(/\r?\n/u))
}
</script>

<template>
  <div class="access-key-policy-fields">
    <div class="access-key-policy-row">
      <div class="access-key-policy-row__label">
        <strong>{{ t('accessKeys.drawer.expiration') }}</strong>
        <p>{{ t('accessKeys.drawer.expirationDescription') }}</p>
      </div>
      <AppSelect
        :model-value="expirationMode"
        :label="t('accessKeys.drawer.expiration')"
        :options="expirationOptions"
        size="sm"
        :disabled="disabled"
        @update:model-value="setExpirationMode"
      />
    </div>

    <FormField
      v-if="expirationMode === 'specified'"
      id="access-key-expires-at"
      :label="t('accessKeys.drawer.expirationTime')"
      :description="t('accessKeys.drawer.expirationTimezone', { timezone: currentTimeZone() })"
      :error="expirationError"
      size="compact"
    >
      <template #default="{ describedBy, invalid }">
        <input
          id="access-key-expires-at"
          :value="expirationInput"
          type="datetime-local"
          step="1"
          :min="minimumExpiration"
          :disabled="disabled"
          :aria-describedby="describedBy"
          :aria-invalid="invalid || undefined"
          @input="setExpiration(($event.target as HTMLInputElement).value)"
        />
      </template>
    </FormField>

    <div class="access-key-policy-row">
      <div class="access-key-policy-row__label">
        <strong>{{ t('accessKeys.drawer.sourceIP') }}</strong>
        <p>{{ t('accessKeys.drawer.sourceIPDescription') }}</p>
      </div>
      <AppSelect
        :model-value="sourceMode"
        :label="t('accessKeys.drawer.sourceIP')"
        :options="sourceOptions"
        size="sm"
        :disabled="disabled"
        @update:model-value="setSourceMode"
      />
    </div>

    <FormField
      v-if="sourceMode === 'restricted'"
      id="access-key-allowed-cidrs"
      :label="t('accessKeys.drawer.allowedCIDRs')"
      :description="t('accessKeys.drawer.allowedCIDRsDescription')"
      :description-warning="t('accessKeys.drawer.proxyIPWarning')"
      :error="cidrError"
      size="compact"
    >
      <template #default="{ describedBy, invalid }">
        <textarea
          id="access-key-allowed-cidrs"
          :value="cidrInput"
          rows="4"
          :placeholder="t('accessKeys.drawer.allowedCIDRsPlaceholder')"
          :disabled="disabled"
          :aria-describedby="describedBy"
          :aria-invalid="invalid || undefined"
          spellcheck="false"
          @input="setCIDRs(($event.target as HTMLTextAreaElement).value)"
        ></textarea>
      </template>
    </FormField>
  </div>
</template>

<style scoped>
.access-key-policy-fields {
  display: grid;
  gap: 12px;
}

.access-key-policy-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3);
}

.access-key-policy-row__label strong {
  display: block;
  font-size: var(--text-meta);
}

.access-key-policy-row__label p {
  margin: 3px 0 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

textarea {
  font-family: var(--font-mono);
}

@media (max-width: 560px) {
  .access-key-policy-row {
    grid-template-columns: 1fr;
  }

  .access-key-policy-row :deep(.app-select__trigger) {
    width: 100%;
  }
}
</style>
