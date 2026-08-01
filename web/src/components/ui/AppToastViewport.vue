<script setup lang="ts">
import { useToast } from '@/app/toast'

import InlineFeedback from './InlineFeedback.vue'

const toast = useToast()
const current = toast.current
</script>

<template>
  <Teleport to="body">
    <Transition name="app-toast" mode="out-in">
      <InlineFeedback
        v-if="current"
        :key="current.id"
        :tone="current.tone"
        appearance="toast"
      >
        {{ current.message }}
      </InlineFeedback>
    </Transition>
  </Teleport>
</template>

<style scoped>
.app-toast-enter-active,
.app-toast-leave-active {
  transition:
    opacity var(--duration-fast) var(--easing-standard),
    transform var(--duration-fast) var(--easing-standard);
}
.app-toast-enter-from,
.app-toast-leave-to {
  opacity: 0;
  transform: translate(-50%, 6px);
}
@media (prefers-reduced-motion: reduce) {
  .app-toast-enter-active,
  .app-toast-leave-active {
    transition: none;
  }
}
</style>
