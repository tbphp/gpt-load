<script setup lang="ts">
import { Eye, EyeOff } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

const props = defineProps<{
  value: string
  revealLabel: string
  concealLabel: string
}>()
const emit = defineEmits<{ reveal: []; conceal: [] }>()
const revealed = ref(false)
const displayValue = computed(() =>
  revealed.value ? props.value : `${props.value.slice(0, 6)}${'•'.repeat(12)}`,
)
watch(
  () => props.value,
  () => (revealed.value = false),
)

function toggle(): void {
  revealed.value = !revealed.value
  if (revealed.value) {
    emit('reveal')
    return
  }
  emit('conceal')
}
</script>

<template>
  <span class="secret-value">
    <code>{{ displayValue }}</code>
    <button
      type="button"
      :aria-label="revealed ? concealLabel : revealLabel"
      :aria-pressed="revealed"
      @click="toggle"
    >
      <EyeOff v-if="revealed" :size="16" aria-hidden="true" />
      <Eye v-else :size="16" aria-hidden="true" />
    </button>
  </span>
</template>

<style scoped>
.secret-value {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}
.secret-value code {
  overflow-wrap: anywhere;
  color: var(--color-code);
}
.secret-value button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  min-width: 44px;
  flex: 0 0 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
</style>
