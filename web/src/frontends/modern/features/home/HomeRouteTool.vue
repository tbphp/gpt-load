<script setup lang="ts">
import { ChevronDown, KeyRound, Route } from '@lucide/vue'
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import type { LogAccessKeyOption } from '@modern/api/logs'
import { positivePage } from '@modern/app/url-state'
import { AppButton, AppIcon, AppOverflowText, AppProtocolTag } from '@modern/components/ui'
import { protocolOrder } from '@modern/i18n/protocols'

const props = defineProps<{ open: boolean; accessKeys?: readonly LogAccessKeyOption[] }>()
defineEmits<{ 'update:open': [boolean] }>()
const { t } = useI18n()
const route = useRoute()
const panelId = useId()
// 摘要读取路由检查保存的参数，与展开后的初始选项保持一致。
const context = computed(() => {
  const query = route.query
  if (
    query.inspect_protocol === undefined &&
    query.inspect_external_model === undefined &&
    query.inspect_access_key_id === undefined
  )
    return undefined
  const keyID = positivePage(query.inspect_access_key_id, 0)
  return {
    protocol:
      protocolOrder.find((protocol) => protocol === query.inspect_protocol) ?? protocolOrder[0],
    model: typeof query.inspect_external_model === 'string' ? query.inspect_external_model : '',
    keyName: keyID
      ? (props.accessKeys?.find((key) => key.id === keyID)?.name ??
        t(props.accessKeys ? 'logs.deleted' : 'ui.loading'))
      : '',
  }
})
</script>

<template>
  <section class="modern-home-tool" :class="{ 'is-open': open }">
    <div class="modern-home-tool-bar">
      <div class="modern-home-tool-title">
        <AppIcon :icon="Route" size="sm" />
        <h2>{{ t('pages.inspector.title') }}</h2>
      </div>
      <div v-if="!open && context" class="modern-home-tool-context">
        <AppProtocolTag :protocol="context.protocol" />
        <AppOverflowText
          v-if="context.model"
          class="modern-home-tool-model"
          :text="context.model"
        />
        <span v-if="context.keyName" class="modern-home-tool-key">
          <AppIcon :icon="KeyRound" size="sm" />
          <AppOverflowText :text="context.keyName" />
        </span>
      </div>
      <AppButton
        variant="text"
        size="xs"
        class="modern-home-tool-toggle"
        :aria-expanded="open"
        :aria-controls="panelId"
        @click="$emit('update:open', !open)"
      >
        {{ t(open ? 'home.collapse' : 'home.expand') }}
        <AppIcon :icon="ChevronDown" size="sm" class="modern-home-tool-chevron" />
      </AppButton>
    </div>
    <div v-if="open" :id="panelId" class="modern-home-tool-body"><slot /></div>
  </section>
</template>

<style scoped>
.modern-home-tool {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
}
.modern-home-tool-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3) var(--modern-space-4);
  padding: var(--modern-space-3) var(--modern-space-4);
}
.modern-home-tool-title {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
}
.modern-home-tool-title h2 {
  color: var(--modern-text);
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
}
.modern-home-tool-context {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-tool-model {
  max-width: 180px;
  font-family: var(--modern-font-mono);
}
.modern-home-tool-key {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  min-width: 0;
  max-width: 160px;
}
.modern-home-tool-toggle {
  margin-inline-start: auto;
  color: var(--modern-accent);
}
.modern-home-tool-chevron {
  transform: rotate(-90deg);
  transition: transform var(--modern-motion-fast) var(--modern-motion-ease);
}
.is-open .modern-home-tool-chevron {
  transform: none;
}
.modern-home-tool-body {
  padding: var(--modern-space-2) var(--modern-space-4) var(--modern-space-4);
}
</style>
