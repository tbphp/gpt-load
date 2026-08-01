<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'

import {
  catalogProviderPresets,
  featuredProviderPresets,
  findProviderPreset,
  type ProviderPreset,
  type ProviderPresetCategory,
  type ProviderPresetID,
} from './channel-presets'

const props = defineProps<{ modelValue: ProviderPresetID; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: ProviderPresetID] }>()
const { t } = useI18n()

const catalogOpen = ref(false)
const search = ref('')
const selectedCatalogPreset = computed(() => {
  const preset = findProviderPreset(props.modelValue)
  return preset?.featured === false ? preset : undefined
})
const catalogSelected = computed(
  () => props.modelValue === 'custom' || selectedCatalogPreset.value !== undefined,
)
const catalogCardMark = computed(() =>
  props.modelValue === 'custom' ? '···' : (selectedCatalogPreset.value?.mark ?? '＋'),
)
const catalogCardName = computed(() =>
  props.modelValue === 'custom'
    ? t('import.presets.custom.name')
    : selectedCatalogPreset.value
      ? t(selectedCatalogPreset.value.nameKey)
      : t('import.presets.more.name'),
)
const catalogCardDescription = computed(() =>
  props.modelValue === 'custom'
    ? t('import.presets.custom.description')
    : selectedCatalogPreset.value
      ? t(selectedCatalogPreset.value.descriptionKey)
      : t('import.presets.more.description'),
)
const categoryOrder: readonly ProviderPresetCategory[] = ['openai-compatible']
const filteredCatalog = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return catalogProviderPresets.filter((preset) => {
    if (!query) return true
    return `${t(preset.nameKey)} ${t(preset.descriptionKey)}`.toLocaleLowerCase().includes(query)
  })
})
const catalogGroups = computed(() =>
  categoryOrder.flatMap((category) => {
    const presets = filteredCatalog.value.filter((preset) => preset.category === category)
    return presets.length ? [{ category, presets }] : []
  }),
)

function choose(id: ProviderPresetID): void {
  if (props.disabled) return
  emit('update:modelValue', id)
  catalogOpen.value = false
}

function toggleCatalog(): void {
  if (props.disabled) return
  catalogOpen.value = !catalogOpen.value
}

function categoryLabel(category: ProviderPresetCategory): string {
  return t(`import.presets.categories.${category}`)
}

function presetSelected(preset: ProviderPreset): boolean {
  return props.modelValue === preset.id
}
</script>

<template>
  <section class="preset-picker" aria-labelledby="provider-presets-heading">
    <header class="preset-picker__header">
      <div>
        <h2 id="provider-presets-heading">{{ t('import.presets.title') }}</h2>
        <p>{{ t('import.presets.description') }}</p>
      </div>
    </header>

    <div class="preset-picker__featured">
      <button
        v-for="preset in featuredProviderPresets"
        :key="preset.id"
        class="preset-picker__choice"
        :class="{ 'preset-picker__choice--selected': presetSelected(preset) }"
        type="button"
        :disabled="disabled"
        :aria-pressed="presetSelected(preset)"
        @click="choose(preset.id)"
      >
        <span class="preset-picker__mark">{{ preset.mark }}</span>
        <strong>{{ t(preset.nameKey) }}</strong>
        <span class="preset-picker__description">{{ t(preset.descriptionKey) }}</span>
        <Check
          v-if="presetSelected(preset)"
          class="preset-picker__selected-icon"
          :size="16"
          aria-hidden="true"
        />
      </button>

      <button
        class="preset-picker__choice"
        :class="{ 'preset-picker__choice--selected': catalogSelected }"
        type="button"
        :disabled="disabled"
        :aria-expanded="catalogOpen"
        aria-controls="provider-preset-catalog"
        @click="toggleCatalog"
      >
        <span class="preset-picker__mark">{{ catalogCardMark }}</span>
        <strong>{{ catalogCardName }}</strong>
        <span class="preset-picker__description">{{ catalogCardDescription }}</span>
        <span v-if="!catalogSelected" class="preset-picker__count">
          {{ catalogProviderPresets.length }}
        </span>
        <Check
          v-if="catalogSelected"
          class="preset-picker__selected-icon"
          :size="16"
          aria-hidden="true"
        />
      </button>
    </div>

    <div v-if="catalogOpen" id="provider-preset-catalog" class="preset-picker__catalog">
      <div class="preset-picker__catalog-toolbar">
        <AppSearchInput
          v-model="search"
          :label="t('import.presets.search')"
          :placeholder="t('import.presets.search')"
          :clear-label="t('import.presets.clearSearch')"
          :disabled="disabled"
        />
        <AppButton variant="ghost" size="compact" :disabled="disabled" @click="catalogOpen = false">
          {{ t('import.presets.collapse') }}
        </AppButton>
      </div>

      <div v-for="group in catalogGroups" :key="group.category" class="preset-picker__group">
        <h3>{{ categoryLabel(group.category) }}</h3>
        <div class="preset-picker__options">
          <button
            v-for="preset in group.presets"
            :key="preset.id"
            class="preset-picker__option"
            type="button"
            :disabled="disabled"
            @click="choose(preset.id)"
          >
            <span class="preset-picker__mark">{{ preset.mark }}</span>
            <span>
              <strong>{{ t(preset.nameKey) }}</strong>
              <small>{{ t(preset.descriptionKey) }}</small>
            </span>
            <span aria-hidden="true">→</span>
          </button>
        </div>
      </div>

      <p v-if="!filteredCatalog.length" class="preset-picker__empty">
        {{ t('import.presets.noMatches') }}
      </p>

      <button
        class="preset-picker__custom"
        type="button"
        :disabled="disabled"
        @click="choose('custom')"
      >
        <span class="preset-picker__mark">···</span>
        <span>
          <strong>{{ t('import.presets.custom.name') }}</strong>
          <small>{{ t('import.presets.custom.description') }}</small>
        </span>
        <span aria-hidden="true">→</span>
      </button>
    </div>
  </section>
</template>

<style scoped>
.preset-picker {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
}

.preset-picker__header {
  margin-bottom: var(--space-3);
}

.preset-picker__header h2,
.preset-picker__header p,
.preset-picker__group h3 {
  margin: 0;
}

.preset-picker__header h2 {
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
}

.preset-picker__header p {
  margin-top: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.preset-picker__featured {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
}

.preset-picker__choice {
  position: relative;
  display: grid;
  min-height: 88px;
  align-content: start;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-3);
  text-align: left;
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}

.preset-picker__choice:hover:not(:disabled),
.preset-picker__option:hover:not(:disabled),
.preset-picker__custom:hover:not(:disabled) {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
}

.preset-picker__choice--selected {
  border-color: var(--color-action);
  box-shadow: inset 3px 0 0 var(--color-action);
}

.preset-picker__choice > strong {
  margin-top: 7px;
  font-size: 12.5px;
  font-weight: 600;
}

.preset-picker__description {
  margin-top: 3px;
  color: var(--color-text-faint);
  font-size: 10.8px;
  line-height: var(--line-normal);
}

.preset-picker__choice:disabled,
.preset-picker__option:disabled,
.preset-picker__custom:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.preset-picker__mark {
  display: grid;
  width: 25px;
  height: 25px;
  place-items: center;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-weight: 700;
}

.preset-picker__count {
  position: absolute;
  top: 11px;
  right: 12px;
  margin: 0;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 10px;
}

.preset-picker__selected-icon {
  position: absolute;
  top: 10px;
  right: 10px;
  color: var(--color-action);
}

.preset-picker__catalog {
  margin-top: 14px;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 14px;
}

.preset-picker__catalog-toolbar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 10px;
}

.preset-picker__group {
  margin-top: 14px;
}

.preset-picker__group h3 {
  margin-bottom: 7px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
  letter-spacing: 0.04em;
}

.preset-picker__options {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}

.preset-picker__option,
.preset-picker__custom {
  display: grid;
  min-width: 0;
  min-height: var(--touch-target);
  grid-template-columns: 28px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 9px 10px;
  text-align: left;
  cursor: pointer;
}

.preset-picker__option strong,
.preset-picker__option small,
.preset-picker__custom strong,
.preset-picker__custom small {
  display: block;
}

.preset-picker__option strong,
.preset-picker__custom strong {
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 600;
}

.preset-picker__option small,
.preset-picker__custom small {
  margin-top: 1px;
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.preset-picker__custom {
  width: 100%;
  margin-top: 8px;
  border-style: dashed;
}

.preset-picker__option .preset-picker__mark,
.preset-picker__custom .preset-picker__mark {
  width: 26px;
  height: 26px;
}

.preset-picker__empty {
  margin: 10px 0 0;
  color: var(--color-text-faint);
  font-size: 11px;
}

@media (max-width: 860px) {
  .preset-picker__featured {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 680px) {
  .preset-picker__options,
  .preset-picker__catalog-toolbar {
    grid-template-columns: 1fr;
  }

  .preset-picker__catalog-toolbar :deep(.app-button) {
    justify-self: start;
  }
}

@media (max-width: 520px) {
  .preset-picker__featured {
    grid-template-columns: 1fr;
  }
}
</style>
