<script setup lang="ts">
import { SquareTerminal } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppChannelIcon, AppIcon, AppOverflowText } from '@modern/components/ui'
import { gatewayClients, gatewayGroups, type GatewayClientID } from './gateway-config'

defineProps<{ selected: GatewayClientID }>()
defineEmits<{ select: [GatewayClientID] }>()
const { t } = useI18n()
const sections = gatewayGroups.map((group) => ({
  group,
  items: gatewayClients.filter((client) => client.group === group),
}))
</script>

<template>
  <nav class="modern-connect-clients" :aria-label="t('home.client')">
    <section v-for="section in sections" :key="section.group">
      <h3 class="modern-connect-clients-label">{{ t('home.clientGroups.' + section.group) }}</h3>
      <ul>
        <li v-for="client in section.items" :key="client.id">
          <AppButton
            variant="ghost"
            size="xs"
            class="modern-connect-client"
            :aria-pressed="client.id === selected"
            @click="$emit('select', client.id)"
          >
            <span class="modern-connect-client-icon">
              <AppIcon v-if="client.id === 'curl'" :icon="SquareTerminal" size="md" />
              <AppChannelIcon
                v-else
                :icon="client.icon"
                :name="client.name"
                size="sm"
                :tooltip="false"
              />
            </span>
            <AppOverflowText class="modern-connect-client-name" :text="client.name" />
          </AppButton>
        </li>
      </ul>
    </section>
  </nav>
</template>

<style scoped>
.modern-connect-clients {
  display: grid;
  align-content: start;
  gap: var(--modern-space-3);
  min-width: 0;
  min-height: 0;
  padding: var(--modern-space-3) var(--modern-space-2);
}
.modern-connect-clients ul {
  display: grid;
  gap: var(--modern-space-0-5);
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-connect-clients-label {
  margin-bottom: var(--modern-space-1);
  padding-inline: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-medium);
}
.modern-connect-client {
  display: flex;
  width: 100%;
  min-width: 0;
  justify-content: flex-start;
  gap: var(--modern-space-2);
  padding: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-text);
  font-weight: var(--modern-weight-regular);
}
.modern-connect-client[aria-pressed='true'],
.modern-connect-client[aria-pressed='true']:hover:not(:disabled, [aria-disabled='true']) {
  background: var(--modern-accent-soft);
  color: var(--modern-action);
  font-weight: var(--modern-weight-medium);
}
.modern-connect-client-icon {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: var(--modern-channel-sm);
}
.modern-connect-client-name {
  flex: 1;
  min-width: 0;
  text-align: start;
}
@container modern-connect (max-width: 620px) {
  .modern-connect-clients {
    display: flex;
    gap: var(--modern-space-1);
    padding: var(--modern-space-2) var(--modern-space-3);
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
    width: auto;
    white-space: nowrap;
  }
}
</style>
