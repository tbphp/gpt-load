<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RequestModel } from '@modern/api/models'
import { AppButton, AppCopyValue, AppIcon, AppProtocolTag, AppTooltip } from '@modern/components/ui'
import { protocolLabel } from '@modern/i18n/protocols'
import { modelGroupCount } from './models-display'
import ModelSourcePreview from './ModelSourcePreview.vue'

const props = defineProps<{
  model: RequestModel
  admin: boolean
  selected?: boolean
  disabled?: boolean
}>()
const emit = defineEmits<{ open: [source?: number] }>()
const { t, n } = useI18n()
const groups = computed(() => modelGroupCount(props.model))
</script>

<template>
  <article class="modern-model-card" :class="{ 'is-selected': selected }" :aria-label="model.name">
    <header class="modern-model-card-heading">
      <h2><AppCopyValue :value="model.name" /></h2>
      <div class="modern-model-card-protocols">
        <AppProtocolTag
          v-for="protocol in model.protocols.slice(0, 2)"
          :key="protocol"
          :protocol="protocol"
        />
        <AppTooltip
          v-if="model.protocols.length > 2"
          :label="
            model.protocols
              .slice(2)
              .map((value) => protocolLabel(value, t))
              .join('\n')
          "
        >
          <span tabindex="0" class="modern-model-card-more">
            +{{ n(model.protocols.length - 2) }}
          </span>
        </AppTooltip>
      </div>
    </header>
    <div class="modern-model-card-sources">
      <div v-if="model.sources.length > 1" class="modern-model-card-source-heading">
        <AppButton variant="text" size="sm" :disabled="disabled" @click="emit('open', 0)">
          {{
            t(model.sources.length > 3 ? 'modelManager.allSources' : 'modelManager.sourceCount', {
              count: n(model.sources.length),
            })
          }}
          <AppIcon :icon="ArrowRight" size="xs" />
        </AppButton>
        <span v-if="admin">{{ t('modelManager.groupCount', { count: n(groups) }) }}</span>
      </div>
      <div class="modern-model-card-source-list">
        <ModelSourcePreview
          v-for="source in model.sources.slice(0, 3)"
          :key="source.price.id"
          :source="source"
          :request-model="model.name"
          :disabled="disabled"
          @open="emit('open', source.price.id)"
        />
      </div>
    </div>
  </article>
</template>

<style scoped>
.modern-model-card {
  display: flex;
  min-width: 0;
  flex-direction: column;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  transition:
    border-color var(--modern-motion-fast) var(--modern-motion-ease),
    box-shadow var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-model-card:hover {
  border-color: var(--modern-control-border-hover);
}
.modern-model-card.is-selected {
  border-color: var(--modern-segmented-active-border);
  box-shadow: var(--modern-shadow-control);
}
.modern-model-card-heading {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
  padding: var(--modern-space-4);
}
.modern-model-card-heading h2 {
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  line-height: var(--modern-leading-compact);
}
.modern-model-card-protocols {
  display: flex;
  min-width: 0;
  min-height: var(--modern-badge-xs);
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-model-card-sources {
  margin-inline: var(--modern-space-4);
  padding-block: var(--modern-space-2);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-model-card-source-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-model-card-source-list {
  display: grid;
}
.modern-model-card-source-list > :not(:last-child) {
  border-bottom: var(--modern-line-width) solid
    color-mix(in srgb, var(--modern-border) 55%, transparent);
  border-bottom-left-radius: 0;
  border-bottom-right-radius: 0;
}
.modern-model-card-more {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
</style>
