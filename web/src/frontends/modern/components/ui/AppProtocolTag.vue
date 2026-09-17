<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { protocolLabel } from '@modern/i18n/protocols'
import AppTag from './AppTag.vue'

const props = withDefaults(
  defineProps<{
    protocol?: string | null
    size?: 'xs' | 'sm'
    removable?: boolean
    disabled?: boolean
  }>(),
  { protocol: undefined, size: 'xs' },
)
defineEmits<{ remove: [] }>()
const { t } = useI18n()
const value = computed(() => props.protocol?.trim().toLowerCase() ?? '')
const label = computed(() => protocolLabel(value.value, t))
</script>

<template>
  <AppTag
    v-if="value"
    class="modern-protocol-tag"
    :data-protocol="value"
    :text="label"
    :size="size"
    :removable="removable"
    :disabled="disabled"
    @remove="$emit('remove')"
  />
  <span v-else class="modern-protocol-empty">—</span>
</template>

<style scoped>
.modern-tag.modern-protocol-tag {
  --modern-protocol-color: var(--modern-muted);
  width: fit-content;
  justify-self: start;
  align-self: center;
  border-color: transparent;
  background: color-mix(in srgb, var(--modern-protocol-color) 5%, var(--modern-surface));
  color: var(--modern-protocol-color);
  font-family: var(--modern-font-mono);
  font-weight: var(--modern-weight-regular);
}
.modern-protocol-tag[data-protocol='openai-completions'] {
  --modern-protocol-color: var(--modern-protocol-completions);
}
.modern-protocol-tag[data-protocol='openai-responses'] {
  --modern-protocol-color: var(--modern-protocol-responses);
}
.modern-protocol-tag[data-protocol='openai-images'] {
  --modern-protocol-color: var(--modern-protocol-images);
}
.modern-protocol-tag[data-protocol='openai-embeddings'] {
  --modern-protocol-color: var(--modern-protocol-embeddings);
}
.modern-protocol-tag[data-protocol='rerank'] {
  --modern-protocol-color: var(--modern-protocol-rerank);
}
.modern-protocol-tag[data-protocol='anthropic'] {
  --modern-protocol-color: var(--modern-protocol-anthropic);
}
.modern-protocol-tag[data-protocol='gemini'] {
  --modern-protocol-color: var(--modern-protocol-gemini);
}
.modern-protocol-empty {
  color: var(--modern-control-placeholder);
}
</style>
