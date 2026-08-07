<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ClientModelDto } from '@/app/resources/models'
import AppTabs, { type AppTabItem } from '@/components/ui/AppTabs.vue'
import CopyButton from '@/components/ui/CopyButton.vue'

import ModelUpstreamPanel from './ModelUpstreamPanel.vue'
import { presentClientModel } from './model-presenter'

const props = defineProps<{
  model: ClientModelDto
}>()
const { t } = useI18n()

const presentation = computed(() => presentClientModel(props.model))

const upstreamCaption = computed(() => {
  const { shape, modelID, count } = presentation.value.upstream
  if (shape === 'multi') return t('models.index.upstreamCount', { count })
  if (shape === 'alias') return t('models.inspector.alias', { model: modelID })
  return t('models.inspector.direct')
})

/** 上游 tab 恒定展示，单上游时也保留一个选中态的 tab。 */
const upstreamTabs = computed<AppTabItem[]>(() =>
  props.model.upstream_models.map((upstream) => ({
    value: upstream.model_id,
    label: upstream.model_id,
    count: upstream.prices.length,
  })),
)

const activeUpstream = ref<string>(props.model.upstream_models[0]?.model_id ?? '')

watch(
  () => props.model.upstream_models,
  (upstreams) => {
    if (upstreams.some((upstream) => upstream.model_id === activeUpstream.value)) return
    activeUpstream.value = upstreams[0]?.model_id ?? ''
  },
)

const panels = ref<InstanceType<typeof ModelUpstreamPanel>[]>([])

/** 每个上游各持有独立编辑器，切换客户端模型前需要逐个确认未保存改动。 */
async function confirmDiscardSwitch(): Promise<boolean> {
  for (const panel of panels.value) {
    if (!(await panel.confirmDiscardSwitch())) return false
  }
  return true
}

defineExpose({ confirmDiscardSwitch })
</script>

<template>
  <div class="model-inspector">
    <header class="model-inspector__identity">
      <div class="model-inspector__name">
        <strong>{{ model.client_model }}</strong>
        <CopyButton
          :value="model.client_model"
          :label="t('models.index.copy', { model: model.client_model })"
          :success-label="t('models.index.copySucceeded')"
          :failure-label="t('models.index.copyFailed')"
        />
      </div>
      <p class="model-inspector__caption">
        <span>{{ upstreamCaption }}</span>
        <span v-for="protocol in model.protocols" :key="protocol" class="model-inspector__protocol">
          {{ t(`common.protocols.${protocol}`) }}
        </span>
      </p>
    </header>

    <!--
      所有面板保持挂载、用 v-show 切换：编辑草稿在 tab 间切换时不丢失，
      因此切 tab 无需确认丢弃（仅切换客户端模型时才确认）。
    -->
    <AppTabs
      v-model="activeUpstream"
      appearance="detail"
      class="model-inspector__tabs"
      :label="t('models.detail.upstreamModel')"
      :items="upstreamTabs"
    >
      <ModelUpstreamPanel
        v-for="upstream in model.upstream_models"
        v-show="upstream.model_id === activeUpstream"
        ref="panels"
        :key="upstream.model_id"
        :upstream="upstream"
      />
    </AppTabs>
  </div>
</template>

<style scoped>
.model-inspector {
  display: grid;
  min-width: 0;
  gap: var(--space-3-25);
}

.model-inspector__identity {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
}

.model-inspector__name {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-1-75);
}

.model-inspector__name strong {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--title-section);
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 共享 CopyButton 默认是 44px 带边框方块，在标题旁过重；此处收紧为无边框图标。 */
.model-inspector__name :deep(.copy-control button) {
  width: 24px;
  height: 24px;
  border: 0;
  background: none;
  color: var(--color-text-faint);
}

.model-inspector__name :deep(.copy-control button:hover) {
  color: var(--color-text);
}

.model-inspector__name :deep(.copy-control svg) {
  width: 14px;
  height: 14px;
}

.model-inspector__caption {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-inspector__protocol {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding: 1px 6px;
}

/* 上游 ID 是等宽标识而非普通标签。 */
.model-inspector__tabs :deep(.app-tabs__trigger) {
  font-family: var(--font-mono);
}
</style>
