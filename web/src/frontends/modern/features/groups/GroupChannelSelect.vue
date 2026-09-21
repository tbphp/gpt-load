<script setup lang="ts">
import { KeyRound, UserRound } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupChannel } from '@modern/api/group-create'
import { channelSearchOption } from '@modern/components/channel-options'
import { AppBadge, AppChannelIcon, AppSearchSelect } from '@modern/components/ui'

const props = defineProps<{
  modelValue: string
  channels: readonly GroupChannel[]
  label: string
  description?: string
  disabled?: boolean
  error?: string
  size?: 'sm'
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const input = ref<InstanceType<typeof AppSearchSelect>>()
defineExpose({ focus: () => input.value?.focus() })
const options = computed(() => props.channels.map(channelSearchOption))
const byID = computed(() => new Map(props.channels.map((channel) => [channel.id, channel])))
const subscription = (id: string) => byID.value.get(id)?.connectionType === 'subscription'
</script>

<template>
  <AppSearchSelect
    ref="input"
    :model-value="modelValue"
    :label="label"
    :description="description"
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
