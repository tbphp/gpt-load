<script setup lang="ts">
import { TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    dirty: boolean
    pending: boolean
    status?: 'idle' | 'saved' | 'error' | 'indeterminate'
    error?: string
    errorPlacement?: 'inline' | 'floating'
    appearance?: 'default' | 'ledger'
    alwaysVisible?: boolean
  }>(),
  {
    status: 'idle',
    error: '',
    errorPlacement: 'inline',
    appearance: 'default',
    alwaysVisible: false,
  },
)

const visible = computed(
  () =>
    props.alwaysVisible || props.dirty || props.pending || props.status !== 'idle' || props.error,
)
const visualState = computed(() => {
  if (props.pending) return 'saving'
  if (props.error || props.status === 'error') return 'error'
  if (props.dirty) return 'dirty'
  if (props.status === 'saved') return 'saved'
  return props.status === 'indeterminate' ? 'indeterminate' : 'idle'
})
</script>

<template>
  <footer
    v-if="visible"
    class="sticky-save-bar"
    :class="[`sticky-save-bar--${appearance}`, `sticky-save-bar--error-${errorPlacement}`]"
    :data-status="visualState"
    :aria-busy="pending || undefined"
  >
    <div class="sticky-save-bar__status" aria-live="polite">
      <slot name="status" :dirty="dirty" :pending="pending" :status="status" :error="error" />
    </div>
    <p v-if="error" class="sticky-save-bar__error" role="alert">
      <TriangleAlert v-if="errorPlacement === 'floating'" :size="16" aria-hidden="true" />
      <span>{{ error }}</span>
    </p>
    <div class="sticky-save-bar__actions">
      <slot name="discard" :disabled="pending" />
      <slot name="save" :disabled="pending" />
    </div>
  </footer>
</template>
