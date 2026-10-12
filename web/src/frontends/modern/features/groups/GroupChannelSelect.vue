<script setup lang="ts">
import { KeyRound, Star, UserRound } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ChannelPartnerTier, GroupChannel } from '@modern/api/group-create'
import { channelSearchOption } from '@modern/components/channel-options'
import { AppBadge, AppChannelIcon, AppSearchSelect, AppTooltip } from '@modern/components/ui'

const props = defineProps<{
  modelValue: string
  channels: readonly GroupChannel[]
  label: string
  hint?: string
  disabled?: boolean
  error?: string
  size?: 'sm'
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const input = ref<InstanceType<typeof AppSearchSelect>>()
defineExpose({ focus: () => input.value?.focus() })
// 合作只改变选择时的位置与标识：首席 → 精选 → 普通合作排在最前，输入关键词后回到原顺序。
const PARTNER_TIER_ORDER: Record<ChannelPartnerTier, number> = {
  principal: 0,
  featured: 1,
  standard: 2,
}
const partnerOrder = (channel: GroupChannel) =>
  channel.partner ? PARTNER_TIER_ORDER[channel.partner.tier] : PARTNER_TIER_ORDER.standard + 1
const options = computed(() => {
  const hasPartners = props.channels.some((channel) => channel.partner)
  return props.channels
    .map((channel, index) => ({ channel, index }))
    .sort((a, b) => partnerOrder(a.channel) - partnerOrder(b.channel) || a.index - b.index)
    .map(({ channel, index }) => ({
      ...channelSearchOption(channel),
      searchOrder: index,
      group: hasPartners
        ? t(channel.partner ? 'groupCreate.channelPartners' : 'groupCreate.channelAll')
        : undefined,
      class: channel.partner?.tier === 'principal' ? 'modern-channel-option--principal' : undefined,
    }))
})
const byID = computed(() => new Map(props.channels.map((channel) => [channel.id, channel])))
const subscription = (id: string) => byID.value.get(id)?.connectionType === 'subscription'
const starLabel = (id: string) => {
  switch (byID.value.get(id)?.partner?.tier) {
    case 'principal':
      return t('groupCreate.principalPartner')
    case 'featured':
      return t('groupCreate.featuredPartner')
    default:
      return ''
  }
}
</script>

<template>
  <AppSearchSelect
    ref="input"
    :model-value="modelValue"
    :label="label"
    :hint="hint"
    :options="options"
    :size="size"
    :disabled="disabled"
    :error="error"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #option="{ option }">
      <AppChannelIcon
        :icon="byID.get(option.value)?.icon"
        :mark="byID.get(option.value)?.mark"
        :name="option.label"
      />
      <span>{{ option.label }}</span>
      <AppTooltip v-if="starLabel(option.value)" :label="starLabel(option.value)">
        <span class="modern-channel-star" role="img" :aria-label="starLabel(option.value)"
          ><Star :size="11" fill="currentColor" aria-hidden="true"
        /></span>
      </AppTooltip>
      <AppBadge
        size="xs"
        :icon="subscription(option.value) ? UserRound : KeyRound"
        :tone="subscription(option.value) ? 'brand' : 'neutral'"
        >{{
          t(subscription(option.value) ? 'subscriptions.connectionType' : 'subscriptions.apiKey')
        }}</AppBadge
      >
    </template>
  </AppSearchSelect>
</template>

<style scoped>
.modern-channel-star {
  display: inline-flex;
  flex: none;
  margin-left: calc(var(--modern-space-1) * -1);
  color: var(--modern-warning);
}
</style>

<style>
/* 下拉面板传送到 body 渲染，首席合作行的金色强调只能用全局样式。 */
.modern-menu-surface
  .modern-menu-option.modern-channel-option--principal:not([data-state='checked']) {
  background: color-mix(in srgb, var(--modern-warning) 9%, transparent);
  box-shadow: inset 2px 0 0 var(--modern-warning);
}
.modern-menu-surface
  .modern-menu-option.modern-channel-option--principal[data-highlighted]:not(
    [data-state='checked']
  ) {
  background: color-mix(in srgb, var(--modern-warning) 15%, var(--modern-control-hover));
}
</style>
