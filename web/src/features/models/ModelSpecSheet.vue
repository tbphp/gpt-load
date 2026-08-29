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
const knownModalities = new Set(['audio', 'embedding', 'image', 'pdf', 'text', 'video'])
const knownStatuses = new Set(['alpha', 'beta', 'deprecated'])

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
    'maxInput',
    model.limits.input === null ? '' : formatInteger(model.limits.input, locale.value),
  )
  push(
    'maxOutput',
    model.limits.output === null ? '' : formatInteger(model.limits.output, locale.value),
  )
  push('modalities', formatModalities())
  return entries
})

/** 目录来源与模型名同属一条溯源信息，合并成一行避免占两行。 */
const sourceLine = computed(() => `${sourceLabel.value} · ${metadata.value.name}`)

const capabilities = computed(() =>
  (['reasoning', 'tool_call', 'structured_output', 'attachment', 'temperature'] as const)
    .filter((capability) => metadata.value.capabilities[capability] === true)
    .map((capability) => t(`models.detail.capabilities.${capability}`))
    .concat(metadata.value.open_weights === true ? [t('models.detail.openWeights')] : []),
)

/** 已知 Models.dev 枚举本地化；新值保留原文，避免目录扩展导致信息丢失。 */
const catalogStatus = computed(() => {
  const status = metadata.value.status
  if (!status || !knownStatuses.has(status)) return status
  return t(`models.detail.status.${status}`)
})

function modalityLabel(modality: string): string {
  return knownModalities.has(modality) ? t(`models.detail.modalities.${modality}`) : modality
}

function formatModalities(): string {
  const { input, output } = metadata.value.modalities
  if (input.length === 0 && output.length === 0) return ''
  const inputLabel = input.map(modalityLabel).join(' / ')
  const outputLabel = output.map(modalityLabel).join(' / ')
  const arrow = outputLabel ? `→ ${outputLabel}` : ''
  return `${inputLabel} ${arrow}`.trim()
}
</script>

<template>
  <div class="model-spec">
    <p class="model-spec__source">{{ sourceLine }}</p>
    <dl v-if="specs.length > 0" class="model-spec__specs">
      <div v-for="spec in specs" :key="spec.key">
        <dt>{{ spec.label }}</dt>
        <dd>{{ spec.value }}</dd>
      </div>
    </dl>
    <ul v-if="capabilities.length > 0 || catalogStatus" class="model-spec__capabilities">
      <li v-for="capability in capabilities" :key="capability">{{ capability }}</li>
      <li v-if="catalogStatus" class="model-spec__status">{{ catalogStatus }}</li>
    </ul>
  </div>
</template>

<style scoped>
.model-spec {
  display: grid;
  min-width: 0;
  gap: var(--space-1-75);
}

.model-spec__source {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

/* label 与值同行内联，避免每项占两行。 */
.model-spec__specs {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3-5);
  margin: 0;
  font-size: var(--text-sm);
}

.model-spec__specs > div {
  display: inline-flex;
  min-width: 0;
  align-items: baseline;
  gap: var(--space-1);
}

.model-spec__specs dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-spec__specs dd {
  margin: 0;
  font-family: var(--font-mono);
}

.model-spec__capabilities {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.model-spec__capabilities li {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  color: var(--color-text-muted);
  padding: 2px 7px;
  font-size: var(--text-label-xs);
}

.model-spec__status {
  background: var(--color-warning-bg);
  color: var(--color-warning);
}
</style>
