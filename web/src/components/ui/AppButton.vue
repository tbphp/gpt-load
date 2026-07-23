<script setup lang="ts">
withDefaults(
  defineProps<{
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
    busy?: boolean
    variant?: 'primary' | 'secondary' | 'ghost'
  }>(),
  {
    type: 'button',
    disabled: false,
    busy: false,
    variant: 'primary',
  },
)
</script>

<template>
  <button
    class="app-button"
    :class="`app-button--${variant}`"
    :type="type"
    :disabled="disabled || busy"
    :aria-busy="busy ? 'true' : undefined"
  >
    <slot />
  </button>
</template>
