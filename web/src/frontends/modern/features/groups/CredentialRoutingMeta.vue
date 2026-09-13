<script setup lang="ts">
import { Network, Scale, Unplug } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import { AppIcon, AppTooltip } from '@modern/components/ui'

const props = withDefaults(defineProps<{ row: CredentialRow; weight?: boolean }>(), {
  weight: true,
})
const { t, n } = useI18n()
const proxyLabel = computed(() =>
  [
    t('credentialCards.proxyOverride', {
      mode: t('credentialCards.proxyMode.' + props.row.proxy.mode),
    }),
    props.row.proxy.mode === 'custom' ? props.row.proxy.display : '',
  ]
    .filter(Boolean)
    .join('\n'),
)
</script>

<template>
  <span
    v-if="(weight && row.weightManual != null) || row.proxy.mode !== 'inherit'"
    class="modern-credential-routing-meta"
  >
    <AppTooltip
      v-if="weight && row.weightManual != null"
      :label="t('credentialCards.manualWeight', { value: n(row.weightManual) })"
    >
      <span class="modern-credential-routing-item" tabindex="0">
        <AppIcon :icon="Scale" size="xs" />{{ n(row.weightManual) }}
      </span>
    </AppTooltip>
    <AppTooltip v-if="row.proxy.mode !== 'inherit'" :label="proxyLabel">
      <span class="modern-credential-routing-item" tabindex="0">
        <AppIcon :icon="row.proxy.mode === 'direct' ? Unplug : Network" size="sm" />
      </span>
    </AppTooltip>
  </span>
</template>

<style scoped>
.modern-credential-routing-meta,
.modern-credential-routing-item {
  display: inline-flex;
  align-items: center;
  flex: none;
  gap: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-credential-routing-meta {
  gap: var(--modern-space-2);
}
.modern-credential-routing-item {
  min-height: var(--modern-space-5);
}
</style>
