<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelCatalogReferenceDto } from '@/app/resources/models'
import { formatInteger } from '@/lib/format'

const props = defineProps<{
  reference: ModelCatalogReferenceDto
}>()
const { locale, t } = useI18n()

interface CatalogSpec {
  key: string
  label: string
  value: string
}

const metadata = computed(() => props.reference.model)

const sourceLabel = computed(() =>
  t(`models.detail.catalogReference.${props.reference.source}`, {
    provider: props.reference.provider_name,
  }),
)

const specs = computed<CatalogSpec[]>(() => {
  const model = metadata.value
  const entries: CatalogSpec[] = []
  const push = (key: string, value: string): void => {
    if (value) entries.push({ key, label: t(`models.detail.specs.${key}`), value })
  }
  push(
    'context',
    model.limits.context === null ? '' : formatInteger(model.limits.context, locale.value),
  )
  push(
    'maxOutput',
    model.limits.output === null ? '' : formatInteger(model.limits.output, locale.value),
  )
  push('modalities', formatModalities())
  push('knowledge', model.knowledge)
  push('released', model.release_date)
  push('family', model.family)
  return entries
})

const capabilities = computed(() =>
  (['reasoning', 'tool_call', 'structured_output', 'attachment', 'temperature'] as const)
    .filter((capability) => metadata.value.capabilities[capability] === true)
    .map((capability) => t(`models.detail.capabilities.${capability}`))
    .concat(metadata.value.open_weights === true ? [t('models.detail.openWeights')] : []),
)

function formatModalities(): string {
  const { input, output } = metadata.value.modalities
  if (input.length === 0 && output.length === 0) return ''
  const arrow = output.length > 0 ? `→ ${output.join(' / ')}` : ''
  return `${input.join(' / ')} ${arrow}`.trim()
}
</script>

<template>
  <div class="model-catalog">
    <p class="model-catalog__source">
      <span>{{ sourceLabel }}</span>
      <strong>{{ metadata.name }}</strong>
    </p>
    <p v-if="metadata.description" class="model-catalog__description">
      {{ metadata.description }}
    </p>
    <dl v-if="specs.length > 0" class="model-catalog__specs">
      <div v-for="spec in specs" :key="spec.key">
        <dt>{{ spec.label }}</dt>
        <dd>{{ spec.value }}</dd>
      </div>
    </dl>
    <ul v-if="capabilities.length > 0" class="model-catalog__capabilities">
      <li v-for="capability in capabilities" :key="capability">{{ capability }}</li>
    </ul>
  </div>
</template>

<style scoped>
.model-catalog {
  display: grid;
  min-width: 0;
  gap: var(--space-2-5);
}

.model-catalog__source {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  margin: 0;
}

.model-catalog__source span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-catalog__source strong {
  font-size: var(--text-meta);
  font-weight: 600;
}

.model-catalog__description {
  max-width: 92ch;
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  line-height: var(--line-normal);
}

.model-catalog__specs {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(118px, max-content));
  gap: var(--space-2) var(--space-6);
  margin: 0;
}

.model-catalog__specs div {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.model-catalog__specs dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-catalog__specs dd {
  margin: 0;
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-catalog__capabilities {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.model-catalog__capabilities li {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  color: var(--color-text-muted);
  padding: 2px 7px;
  font-size: var(--text-label-xs);
}
</style>
