<script setup lang="ts">
import { Route } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProxyViewDto } from '@/api/control/types'
import AppTooltip from '@/components/ui/AppTooltip.vue'

const props = defineProps<{ view: ProxyViewDto }>()
const { t } = useI18n()

// 只在凭据自己配置了代理（未沿用上一级）时提示，继承态不加视觉噪音。
const own = computed(() => props.view.configured_mode !== 'inherit')
// 只报类型，具体地址留给展开后的代理面板，避免在列表层面泄露/堆叠细节。
const tooltip = computed(() =>
  t('common.proxy.ownTooltip', {
    type: t(`common.proxy.mode.${props.view.configured_mode}`),
  }),
)
</script>

<template>
  <AppTooltip v-if="own" :content="tooltip">
    <span class="proxy-scope-indicator" tabindex="0" :aria-label="tooltip">
      <Route :size="13" aria-hidden="true" />
    </span>
  </AppTooltip>
</template>

<style scoped>
.proxy-scope-indicator {
  display: inline-grid;
  width: 22px;
  height: 22px;
  flex: none;
  place-items: center;
  border-radius: var(--radius-tag);
  background: var(--color-info-bg);
  color: var(--color-info);
  cursor: help;
}

.proxy-scope-indicator:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
</style>
