<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppOverflowText, AppProtocolTag, AppTag } from '@modern/components/ui'
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
const sections = computed(() =>
  gatewayGroups.map((group) => ({
    group,
    items: props.clients.filter((client) => client.group === group),
  })),
)
</script>

<template>
  <div class="modern-connect-clients">
    <section v-for="section in sections" :key="section.group">
      <h3 class="modern-connect-clients-label">{{ t('home.clientGroups.' + section.group) }}</h3>
      <ul>
        <li v-for="client in section.items" :key="client.id">
          <AppButton
            variant="ghost"
            size="sm"
            class="modern-connect-client"
            :disabled="!client.supported"
            :aria-pressed="client.id === selected"
            @click="$emit('select', client.id)"
          >
            <AppOverflowText class="modern-connect-client-name" :text="client.name" />
            <AppProtocolTag
              v-if="client.supported && client.protocol"
              :protocol="client.protocol"
              short
            />
            <AppTag
              v-else
              size="xs"
              tone="neutral"
              :text="t(client.supported ? 'home.byTarget' : 'home.protocolClosed')"
            />
          </AppButton>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.modern-connect-clients {
  display: grid;
  align-content: start;
  gap: var(--modern-space-4);
  min-width: 0;
  padding: var(--modern-space-4) var(--modern-space-3) var(--modern-space-5);
}
.modern-connect-clients ul {
  display: grid;
  gap: var(--modern-space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-connect-clients-label {
  margin-bottom: var(--modern-space-2);
  padding-inline: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-medium);
  letter-spacing: var(--modern-tracking-label);
}
/* 按钮本身是居中的行内控件，列表里要变成整行、左对齐的目录项。 */
.modern-connect-client {
  display: flex;
  width: 100%;
  min-width: 0;
  justify-content: space-between;
  gap: var(--modern-space-2);
  padding: var(--modern-space-2);
  color: var(--modern-text);
  font-weight: var(--modern-weight-regular);
}
.modern-connect-client[aria-pressed='true'] {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-weight: var(--modern-weight-medium);
}
.modern-connect-client-name {
  min-width: 0;
  flex: 1;
  text-align: start;
}
@container modern-connect (max-width: 720px) {
  .modern-connect-clients {
    display: flex;
    overflow-x: auto;
    gap: var(--modern-space-2);
    padding: var(--modern-space-3) var(--modern-space-5);
    scrollbar-gutter: var(--modern-scrollbar-gutter);
  }
  .modern-connect-clients-label {
    display: none;
  }
  .modern-connect-clients section,
  .modern-connect-clients ul {
    display: contents;
  }
  .modern-connect-clients li {
    flex: none;
  }
  .modern-connect-client {
    display: grid;
    justify-items: start;
    gap: var(--modern-space-1);
    width: auto;
    min-width: 132px;
    padding-inline: var(--modern-space-3);
  }
}
</style>
