<script setup lang="ts">
import { Eye, EyeOff } from '@lucide/vue'
import { computed, onDeactivated, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { credentialDisplayText, maskSubscriptionAccount } from '@shared/credential-display'
import { AppCopyValue, AppIconButton, AppOverflowText } from './ui'

const props = defineProps<{
  name?: string
  value: string
  subscription?: boolean
  detail?: boolean
  reveal?: boolean
  copy?: boolean
  copyLabel?: string
  resolveValue?: () => string | Promise<string>
}>()
const { t } = useI18n()
const revealed = ref(false)
watch(
  () => [props.value, props.name, props.reveal],
  () => {
    revealed.value = false
  },
)
onDeactivated(() => {
  revealed.value = false
})
const accountValue = computed(() =>
  props.subscription && !revealed.value ? maskSubscriptionAccount(props.value) : props.value,
)
const display = computed(() =>
  props.detail
    ? accountValue.value
    : credentialDisplayText(
        props.name,
        props.value,
        props.subscription ? 'subscription' : 'api_key',
      ),
)
</script>

<template>
  <span class="modern-credential-display">
    <AppOverflowText v-if="detail && name" class="modern-credential-display-name" :text="name" />
    <span class="modern-credential-display-value">
      <AppCopyValue
        v-if="copy"
        :value="value"
        :display="display"
        :label="copyLabel"
        :resolve-value="resolveValue"
      />
      <AppOverflowText v-else :text="display" />
      <AppIconButton
        v-if="subscription && detail && reveal && value"
        :icon="revealed ? EyeOff : Eye"
        :label="t(revealed ? 'credentialCards.hideAccount' : 'credentialCards.showAccount')"
        :aria-pressed="revealed"
        size="xs"
        variant="text"
        @click.stop="revealed = !revealed"
      />
    </span>
  </span>
</template>

<style scoped>
.modern-credential-display {
  display: inline-flex;
  flex-direction: column;
  min-width: 0;
  max-width: 100%;
  vertical-align: middle;
}
.modern-credential-display-name {
  font-family: var(--modern-font-sans);
  font-weight: var(--modern-weight-semibold);
  color: var(--modern-text);
}
.modern-credential-display-value {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-0-5);
}
</style>
