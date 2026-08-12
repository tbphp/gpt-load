<script setup lang="ts">
import { computed } from 'vue'

import { namespacedChannelIconMarkup, nextChannelIconInstanceId } from './channel-icons'

const props = defineProps<{
  icon: string
  mark: string
}>()

const instanceId = nextChannelIconInstanceId()

const markup = computed(() => namespacedChannelIconMarkup(props.icon, instanceId))
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- markup is our own vendored, build-time SVG asset, not user input -->
  <span v-if="markup" class="channel-icon" aria-hidden="true" v-html="markup" />

  <span v-else class="channel-icon channel-icon--fallback" aria-hidden="true">{{ mark }}</span>
</template>

<style scoped>
.channel-icon {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  line-height: 1;
}

.channel-icon svg {
  display: block;
  width: 1em;
  height: 1em;
}

.channel-icon--fallback {
  min-width: 1.7em;
  height: 1.3em;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 0 3px;
  font-family: var(--font-mono);
  font-size: 0.7em;
  font-weight: 700;
}
</style>
