<script setup lang="ts">
import { ChevronDown, KeyRound, Route } from '@lucide/vue'
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppIcon, AppOverflowText, AppProtocolTag } from '@modern/components/ui'
import type { GatewaySelection } from './gateway-config'

defineProps<{ open: boolean; selection?: GatewaySelection }>()
defineEmits<{ 'update:open': [boolean] }>()
const { t } = useI18n()
const panelId = useId()
</script>

<template>
  <section class="modern-home-tool" :class="{ 'is-open': open }">
    <div class="modern-home-tool-bar">
      <div class="modern-home-tool-title">
        <AppIcon :icon="Route" size="sm" />
        <h2>{{ t('pages.inspector.title') }}</h2>
      </div>
      <div v-if="!open && selection" class="modern-home-tool-context">
        <AppProtocolTag v-if="selection.protocol" :protocol="selection.protocol" />
        <AppOverflowText
          v-if="selection.model"
          class="modern-home-tool-model"
          :text="selection.model"
        />
        <span v-if="selection.keyName" class="modern-home-tool-key">
          <AppIcon :icon="KeyRound" size="sm" />
          <AppOverflowText :text="selection.keyName" />
        </span>
      </div>
      <AppButton
        variant="text"
        size="sm"
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
  padding: var(--modern-space-4) var(--modern-space-5);
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
}
.modern-home-tool-chevron {
  transform: rotate(-90deg);
  transition: transform var(--modern-motion-fast) var(--modern-motion-ease);
}
.is-open .modern-home-tool-chevron {
  transform: none;
}
.modern-home-tool-body {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: 0 var(--modern-space-5) var(--modern-space-5);
}
</style>
