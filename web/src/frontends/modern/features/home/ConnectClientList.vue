<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { AppButton, AppTag } from '@modern/components/ui'
import { gatewayGroups, type GatewayClientID, type GatewayGroupID } from './gateway-config'

export interface ClientEntry {
  id: GatewayClientID
  name: string
  group: GatewayGroupID
  protocol: string
  supported: boolean
}
const props = defineProps<{ clients: ClientEntry[]; selected: GatewayClientID }>()
defineEmits<{ select: [GatewayClientID] }>()
const { t } = useI18n()
const sections = gatewayGroups.map((group) => ({
  group,
  items: props.clients.filter((client) => client.group === group),
}))
</script>

<template>
  <div class="modern-connect-clients">
    <template v-for="section in sections" :key="section.group">
      <p class="modern-connect-clients-label">{{ t('home.clientGroups.' + section.group) }}</p>
      <ul>
        <li v-for="client in section.items" :key="client.id">
          <AppButton
            variant="ghost"
            size="sm"
            :disabled="!client.supported"
            :aria-pressed="client.id === selected"
            @click="$emit('select', client.id)"
          >
            <span>{{ client.name }}</span>
            <!-- 目录里只回答「能不能用」。协议名（openai-responses 这种）有 16 个字符，
                 200px 的列放不下，硬塞会把客户端名挤没；选中后的提示会写清是哪个协议。 -->
            <AppTag
              v-if="!client.supported"
              size="xs"
              tone="neutral"
              :text="t('home.protocolClosed')"
            />
          </AppButton>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.modern-connect-clients {
  padding: var(--modern-space-1-5) var(--modern-space-2) var(--modern-space-3);
}
.modern-connect-clients ul {
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-connect-clients-label {
  padding: var(--modern-space-2) var(--modern-space-2) var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  letter-spacing: var(--modern-tracking-label);
}
/* 按钮本身是居中的行内控件，列表里要变成整行、左对齐的目录项。 */
.modern-connect-clients :deep(.modern-button) {
  width: 100%;
  justify-content: flex-start;
  gap: var(--modern-space-2);
  font-weight: var(--modern-weight-regular);
}
.modern-connect-clients :deep(.modern-button[aria-pressed='true']) {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-weight: var(--modern-weight-medium);
}
.modern-connect-clients :deep(.modern-button > span:first-child) {
  overflow: hidden;
  flex: 1;
  min-width: 0;
  text-align: start;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 面板窄到无法分栏时，目录从纵向目录退化成横排；10 行纵向列表在窄屏上太占地方。
分组标题一起隐藏：横排里它会把每组顶到新的一行，反而更乱。 */
@container modern-connect (max-width: 620px) {
  .modern-connect-clients {
    display: flex;
    flex-wrap: wrap;
    gap: var(--modern-space-1-5);
    padding: var(--modern-space-3) var(--modern-space-4);
  }
  .modern-connect-clients-label {
    display: none;
  }
  .modern-connect-clients ul {
    display: contents;
  }
  .modern-connect-clients :deep(.modern-button) {
    width: auto;
    border: var(--modern-line-width) solid var(--modern-border);
    border-radius: var(--modern-radius-round);
  }
}
</style>
