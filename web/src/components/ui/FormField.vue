<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  id: string
  label: string
  description?: string
  error?: string
}>()

const descriptionId = computed(() => (props.description ? `${props.id}-description` : undefined))
const errorId = computed(() => (props.error ? `${props.id}-error` : undefined))
const describedBy = computed(
  () => [descriptionId.value, errorId.value].filter(Boolean).join(' ') || undefined,
)
</script>

<template>
  <div class="form-field">
    <label class="form-field__label" :for="id">{{ label }}</label>
    <slot :described-by="describedBy" :description-id="descriptionId" :error-id="errorId" />
    <p v-if="description" :id="descriptionId" class="form-field__description">
      {{ description }}
    </p>
    <p v-if="error" :id="errorId" class="form-field__error" role="alert">
      <span aria-hidden="true">▲</span>
      <span>{{ error }}</span>
    </p>
  </div>
</template>
