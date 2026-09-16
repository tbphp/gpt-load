<script setup lang="ts">
import { Copy, SquareTerminal } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppCopyValue, AppIcon } from '@modern/components/ui'
import type { TerminalLine } from './gateway-config'

defineProps<{
  lines: TerminalLine[]
  content: string
  resolve: () => Promise<string>
  copyable: boolean
  note?: string
}>()
const { t } = useI18n()
</script>

<template>
  <div class="modern-connect-terminal">
    <header>
      <AppIcon :icon="SquareTerminal" size="sm" />
      <span>{{ t('home.terminal') }}</span>
      <AppCopyValue v-if="copyable" :value="content" :resolve-value="resolve">
        <template #trigger="{ copy, pending }">
          <AppButton size="xs" variant="ghost" :icon="Copy" :loading="pending" @click="copy()">{{
            t('home.copyAll')
          }}</AppButton>
        </template>
      </AppCopyValue>
    </header>
    <div class="modern-connect-lines" tabindex="0" role="group" :aria-label="t('home.terminal')">
      <p v-for="(line, index) in lines" :key="index" :class="{ 'is-continued': !line.command }">
        <!-- 提示符只是排版记号，读屏时跳过，否则每行都会多念一个美元符。 -->
        <i aria-hidden="true">{{ line.command ? '$' : '' }}</i
        ><span>{{ line.text }}</span>
      </p>
    </div>
    <p v-if="note" class="modern-connect-note">{{ note }}</p>
  </div>
</template>

<style scoped>
.modern-connect-terminal {
  display: grid;
  min-width: 0;
  overflow: hidden;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
.modern-connect-terminal > header {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-1-5) var(--modern-space-2) var(--modern-space-1-5)
    var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-connect-terminal > header > :last-child {
  margin-inline-start: auto;
}
.modern-connect-lines {
  overflow-x: auto;
  padding: var(--modern-space-3);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  scrollbar-gutter: var(--modern-scrollbar-gutter);
}
/* 空行也要占一行高，否则 heredoc 里的空行会塌掉，粘贴出来的内容和看到的对不上。 */
.modern-connect-lines p {
  display: flex;
  gap: var(--modern-space-1-5);
  min-height: 1lh;
  white-space: pre;
}
.modern-connect-lines i {
  width: var(--modern-space-2);
  flex: none;
  color: var(--modern-accent);
  font-style: normal;
}
.modern-connect-lines .is-continued {
  color: var(--modern-muted);
}
.modern-connect-note {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
</style>
