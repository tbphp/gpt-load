<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import {
  AppBadge,
  AppButton,
  AppCheckbox,
  AppCopyValue,
  AppOverflowText,
  AppSwitch,
  AppTooltip,
} from '@modern/components/ui'
import { credentialStatus, credentialTime } from './credential-presentation'
import CredentialCardActions from './CredentialCardActions.vue'
import CredentialCardFrame from './CredentialCardFrame.vue'
import CredentialOutcomeSummary from './CredentialOutcomeSummary.vue'
import CredentialRoutingMeta from './CredentialRoutingMeta.vue'

const props = defineProps<{
  row: CredentialRow
  selected: boolean
  disabled: boolean
  pending?: boolean
  error?: string
  resolveSecret: () => Promise<string>
}>()
defineEmits<{ select: [value: boolean]; toggle: [value: boolean]; action: [value: string] }>()
const { t, n, locale } = useI18n()
const state = computed(() => credentialStatus(props.row))
const issues = computed(() =>
  [
    props.row.cooldownUntil
      ? t('groupDetail.recoversAt', { time: credentialTime(props.row.cooldownUntil, locale.value) })
      : '',
    props.row.failuresInRow
      ? t('credentialCards.consecutiveFailures', { count: n(props.row.failuresInRow) })
      : '',
    props.row.modelCooldowns.length
      ? t('credentialCards.modelCooldowns', { count: n(props.row.modelCooldowns.length) })
      : '',
  ]
    .filter(Boolean)
    .join(' · '),
)
</script>
<template>
  <CredentialCardFrame :selected="selected" :pending="pending" compact>
    <template #heading>
      <AppTooltip :label="t('groupDetail.selectCredential', { name: row.mask })"
        ><AppCheckbox
          :model-value="selected"
          :label="t('groupDetail.selectCredential', { name: row.mask })"
          label-hidden
          :disabled="disabled"
          @update:model-value="$emit('select', $event)"
      /></AppTooltip>
      <div class="modern-api-card-secret">
        <AppCopyValue
          :key="row.secretVersion"
          :value="row.mask"
          :resolve-value="resolveSecret"
          :label="t('credentialCards.copyKey')"
        />
      </div>
      <AppTooltip :label="issues || undefined">
        <AppButton
          variant="text"
          size="xs"
          :disabled="disabled"
          :aria-label="`${t(state.key)} · ${t('credentialCards.diagnosticsAndSettings')}`"
          @click="$emit('action', 'details')"
        >
          <AppBadge :tone="state.tone" variant="plain" size="xs" dot
            ><AppOverflowText :text="t(state.key)"
          /></AppBadge>
        </AppButton>
      </AppTooltip>
    </template>
    <dl class="modern-api-card-metadata">
      <div>
        <dt>{{ t('groupDetail.lastUsed') }}</dt>
        <dd><AppOverflowText :text="credentialTime(row.lastUsed, locale)" /></dd>
      </div>
      <div>
        <dt>{{ t('credentialCards.weight') }}</dt>
        <dd>{{ n(row.weight) }}<CredentialRoutingMeta :row="row" :weight="false" /></dd>
      </div>
    </dl>
    <template #footer
      ><CredentialOutcomeSummary :usage="row.daily" compact />
      <div class="modern-api-card-actions">
        <CredentialCardActions
          :row="row"
          :disabled="disabled"
          @action="$emit('action', $event)"
        /><AppSwitch
          size="xxs"
          :model-value="row.enabled"
          :label="t('groups.edit.enabled')"
          :disabled="disabled"
          @update:model-value="$emit('toggle', $event)"
        /></div
    ></template>
  </CredentialCardFrame>
</template>
<style scoped>
.modern-api-card-secret {
  flex: 1;
  min-width: 0;
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-secondary);
}
.modern-api-card-metadata {
  display: grid;
  grid-template-columns: minmax(0, 1.7fr) minmax(0, 1fr);
  gap: var(--modern-space-2);
  margin: 0;
}
.modern-api-card-metadata > div {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  min-width: 0;
}
.modern-api-card-metadata dt {
  flex: none;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-api-card-metadata dd {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  min-height: var(--modern-space-5);
  min-width: 0;
  margin: 0;
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-api-card-actions {
  display: flex;
  align-items: center;
  flex: none;
  gap: var(--modern-space-0-5);
  margin-left: auto;
}
</style>
