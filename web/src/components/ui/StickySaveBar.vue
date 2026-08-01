<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    dirty: boolean
    pending: boolean
    status?: 'idle' | 'saved' | 'error' | 'indeterminate'
    error?: string
  }>(),
  { status: 'idle', error: '' },
)

const visible = computed(
  () => props.dirty || props.pending || props.status !== 'idle' || props.error,
)
</script>

<template>
  <footer
    v-if="visible"
    class="sticky-save-bar"
    :data-status="status"
    :aria-busy="pending || undefined"
  >
    <div class="sticky-save-bar__status" aria-live="polite">
      <slot name="status" :dirty="dirty" :pending="pending" :status="status" :error="error" />
    </div>
    <p v-if="error" class="sticky-save-bar__error" role="alert">{{ error }}</p>
    <div class="sticky-save-bar__actions">
      <slot name="discard" :disabled="pending" />
      <slot name="save" :disabled="pending" />
    </div>
  </footer>
</template>
