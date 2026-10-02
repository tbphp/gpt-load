<script setup lang="ts">
import { Eye, EyeOff } from '@lucide/vue'
import { computed, onDeactivated, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { credentialDisplayText, maskSubscriptionAccount } from '@shared/credential-display'
import AppTooltip from './ui/AppTooltip.vue'
import OverflowTooltip from './ui/OverflowTooltip.vue'

const props = defineProps<{
  name?: string
  value: string
  subscription?: boolean
  detail?: boolean
  reveal?: boolean
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
const display = computed(() =>
  props.detail
    ? props.subscription && !revealed.value
      ? maskSubscriptionAccount(props.value)
      : props.value
    : credentialDisplayText(
        props.name,
        props.value,
        props.subscription ? 'subscription' : 'api_key',
      ),
)
</script>

<template>
  <span class="credential-display">
    <OverflowTooltip v-if="detail && name" as="strong" :content="name">{{ name }}</OverflowTooltip>
    <span class="credential-display-value">
      <OverflowTooltip as="span" :content="display">{{ display }}</OverflowTooltip>
      <AppTooltip
        v-if="subscription && detail && reveal && value"
        :content="t(revealed ? 'group.credentials.hideAccount' : 'group.credentials.showAccount')"
      >
        <button
          type="button"
          :aria-label="
            t(revealed ? 'group.credentials.hideAccount' : 'group.credentials.showAccount')
          "
          :aria-pressed="revealed"
          @click.stop="revealed = !revealed"
        >
          <EyeOff v-if="revealed" :size="14" aria-hidden="true" />
          <Eye v-else :size="14" aria-hidden="true" />
        </button>
      </AppTooltip>
    </span>
  </span>
</template>

<style scoped>
.credential-display {
  display: inline-flex;
  flex-direction: column;
  min-width: 0;
  max-width: 100%;
  vertical-align: middle;
}
.credential-display-value {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  gap: var(--space-1);
}
.credential-display-value button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  padding: var(--space-1);
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
.credential-display-value button:hover {
  color: var(--color-text);
  background: var(--color-interactive-hover);
}
.credential-display strong {
  color: var(--color-text);
  font-family: var(--font-sans);
}
</style>
