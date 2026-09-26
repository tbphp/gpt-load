<script setup lang="ts">
import { computed, ref } from 'vue'
import { CircleHelp } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { GroupChannel } from '@modern/api/group-create'
import { AppIcon, AppOverflowText, AppTextField, AppTooltip } from '@modern/components/ui'
import { validBaseURL } from './group-create-rules'

const props = defineProps<{
  channel: GroupChannel
  disabled?: boolean
  error?: string
  size?: 'sm'
}>()
const model = defineModel<string>({ required: true })
const { t } = useI18n()
const input = ref<InstanceType<typeof AppTextField>>()
defineExpose({ focus: () => input.value?.focus() })
const defaults = computed(() =>
  props.channel.defaultBaseURLs.length
    ? props.channel.defaultBaseURLs
    : props.channel.defaultBaseURL
      ? [props.channel.defaultBaseURL]
      : [],
)
const required = computed(
  () => props.channel.fields.find((field) => field.key === 'base_url')?.required,
)
const gateway = computed(() =>
  ['gpt_load', 'newapi', 'cliproxyapi', 'sub2api'].includes(props.channel.id),
)
const subscription = computed(() => props.channel.connectionType === 'subscription')
const description = computed(() => {
  const help = subscription.value
    ? 'subscriptionURLHelp'
    : gateway.value
      ? 'gatewayURLHelp'
      : props.channel.id === 'openai_compatible'
        ? 'compatibleURLHelp'
        : 'baseURLHelp'
  return [
    t(`groupCreate.${help}`),
    !required.value && defaults.value.length ? t('groupCreate.defaultURLHelp') : '',
  ]
    .filter(Boolean)
    .join(' ')
})
function versionPath(url: string): string | undefined {
  return new URL(url).pathname
    .split('/')
    .reverse()
    .find((segment) => /^v\d+[a-z]*$/iu.test(segment))
    ?.toLowerCase()
}
const warning = computed(() => {
  const value = model.value.trim()
  if (!value || !validBaseURL(value) || subscription.value) return undefined
  if (gateway.value)
    return /\/v1(?:beta)?\/?$/iu.test(new URL(value).pathname)
      ? t('groupCreate.gatewayURLWarning')
      : undefined
  const references = defaults.value.filter(validBaseURL)
  return references.length &&
    references.every((reference) => versionPath(reference) !== versionPath(value))
    ? t('groupCreate.urlVersionWarning')
    : undefined
})
</script>

<template>
  <AppTextField
    ref="input"
    v-model="model"
    :label="t('groupCreate.baseURL')"
    :description-warning="warning"
    :placeholder="
      defaults[0] ||
      (channel.id === 'openai_compatible' ? 'https://api.example.com/v1' : 'https://')
    "
    :required="required"
    :disabled="disabled"
    :error="error"
    :size="size"
    type="url"
    autocomplete="off"
    autocapitalize="none"
    spellcheck="false"
  >
    <template #label-extra>
      <AppOverflowText
        v-if="defaults.length"
        :text="t('groupCreate.defaultURLs', { urls: defaults.join(', ') })"
      />
      <AppTooltip :label="description">
        <span class="modern-base-url-help" tabindex="0" role="img" :aria-label="description">
          <AppIcon :icon="CircleHelp" size="sm" />
        </span>
      </AppTooltip>
    </template>
  </AppTextField>
</template>

<style scoped>
.modern-base-url-help {
  display: inline-flex;
  flex-shrink: 0;
  border-radius: var(--modern-radius-control);
  cursor: help;
}
.modern-base-url-help:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
}
</style>
