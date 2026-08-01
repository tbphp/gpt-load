<script setup lang="ts">
import { computed, ref, useId } from 'vue'

export interface SectionNavItem {
  id: string
  label: string
  disabled?: boolean
}

const props = defineProps<{
  items: readonly SectionNavItem[]
  modelValue: string
  label: string
}>()

const emit = defineEmits<{
  'update:modelValue': [id: string]
}>()

const expanded = ref(false)
const optionsId = `${useId()}-options`
const current = computed(
  () => props.items.find((item) => item.id === props.modelValue) ?? props.items[0],
)

function select(id: string): void {
  emit('update:modelValue', id)
  expanded.value = false
}
</script>

<template>
  <nav class="section-nav" :aria-label="label">
    <div class="section-nav__mobile">
      <button
        class="section-nav__toggle"
        type="button"
        :aria-expanded="expanded"
        :aria-controls="optionsId"
        @click="expanded = !expanded"
      >
        <span>{{ current?.label }}</span>
        <span aria-hidden="true">⌄</span>
      </button>
      <ul v-if="expanded" :id="optionsId" class="section-nav__options">
        <li v-for="item in items" :key="item.id">
          <button
            type="button"
            :aria-current="item.id === modelValue ? 'page' : undefined"
            :disabled="item.disabled"
            @click="select(item.id)"
          >
            {{ item.label }}
          </button>
        </li>
      </ul>
    </div>

    <ul class="section-nav__desktop">
      <li v-for="item in items" :key="item.id">
        <a
          :href="`#${item.id}`"
          :aria-current="item.id === modelValue ? 'location' : undefined"
          :aria-disabled="item.disabled ? 'true' : undefined"
          @click.prevent="!item.disabled && select(item.id)"
        >
          {{ item.label }}
        </a>
      </li>
    </ul>
  </nav>
</template>
