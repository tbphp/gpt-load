<script setup lang="ts">
import { Copy } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppCopyValue, AppIconButton, AppOverflowText, AppSvg } from '@modern/components/ui'
import type { GatewaySlot, MirrorRow } from './gateway-config'

interface MirrorValue {
  slot: GatewaySlot
  label: string
  value: string
  secret: boolean
}
const props = defineProps<{
  rows: MirrorRow[]
  values: MirrorValue[]
  title: string
  caption: string
  copyable: boolean
  resolveKey: () => Promise<string>
}>()
const { t, n } = useI18n()
/* 示意图按行排布，行高与首行偏移固定，整幅高度由行数决定。 */
const rowHeight = 26
const top = 34
const height = computed(() => top + props.rows.length * rowHeight + 12)
const order = computed(() => new Map(props.values.map((value, index) => [value.slot, index + 1])))
const marks = computed(() =>
  props.rows.map((row, index) => ({
    label: row.label,
    y: top + index * rowHeight,
    number: row.slot ? order.value.get(row.slot) : undefined,
  })),
)
</script>

<template>
  <div class="modern-connect-mirror">
    <figure>
      <AppSvg :viewBox="`0 0 260 ${height}`" focusable="false" role="img" :aria-label="caption">
        <rect class="modern-mirror-frame" x="0.5" y="0.5" width="259" :height="height - 1" />
        <path class="modern-mirror-line" d="M0 23h260" />
        <circle class="modern-mirror-dot" cx="13" cy="12" r="3.2" />
        <circle class="modern-mirror-dot" cx="24" cy="12" r="3.2" />
        <circle class="modern-mirror-dot" cx="35" cy="12" r="3.2" />
        <!-- 标题栏写出这是哪个客户端的哪个界面，只画三个点等于没说。 -->
        <text class="modern-mirror-title" x="48" y="15.5">{{ title }}</text>
        <path class="modern-mirror-line" :d="`M46 23v${height - 23}`" />
        <rect
          v-for="index in 4"
          :key="index"
          class="modern-mirror-rail"
          x="10"
          :y="34 + (index - 1) * 15"
          width="26"
          height="5"
          rx="2.5"
        />
        <template v-for="(mark, index) in marks" :key="index">
          <text v-if="mark.label" class="modern-mirror-label" x="58" :y="mark.y + 6">{{
            mark.label
          }}</text>
          <rect
            v-else
            class="modern-mirror-rail"
            x="58"
            :y="mark.y + 1"
            width="38"
            height="4"
            rx="2"
          />
          <rect
            class="modern-mirror-field"
            :class="{ 'is-marked': mark.number }"
            x="58"
            :y="mark.y + 10"
            width="176"
            height="13"
            rx="4"
          />
          <template v-if="mark.number">
            <circle class="modern-mirror-marker" cx="247" :cy="mark.y + 16" r="7.5" />
            <text class="modern-mirror-number" x="247" :y="mark.y + 19">{{ mark.number }}</text>
          </template>
        </template>
      </AppSvg>
      <figcaption>{{ caption }}</figcaption>
    </figure>
    <div class="modern-connect-guide">
      <p>{{ t('home.mirrorGuide') }}</p>
      <ol class="modern-connect-values">
        <li v-for="(item, index) in values" :key="item.slot">
          <span class="modern-connect-number" aria-hidden="true">{{ n(index + 1) }}</span>
          <div>
            <h3>{{ item.label }}</h3>
            <AppOverflowText
              :text="item.value || t('home.modelPending')"
              class="modern-connect-value"
            />
          </div>
          <AppCopyValue
            v-if="copyable && item.value"
            :value="item.value"
            :resolve-value="item.secret ? resolveKey : undefined"
          >
            <template #trigger="{ copy, pending }">
              <AppIconButton
                size="xs"
                variant="ghost"
                :icon="Copy"
                :loading="pending"
                :label="t('home.copyField', { field: item.label })"
                @click="copy()"
              />
            </template>
          </AppCopyValue>
        </li>
      </ol>
    </div>
  </div>
</template>

<style scoped>
/* 这两个是示意图的绘图单位（viewBox 尺度），不是界面字号：
   整幅图缩到约 230px 宽，套用 caption 的 11px 会比框还高。 */
.modern-connect-mirror {
  --modern-mirror-label-size: 9px;
  --modern-mirror-number-size: 9px;
  display: grid;
  grid-template-columns: minmax(0, 240px) minmax(0, 1fr);
  align-items: start;
  gap: var(--modern-space-5);
}
.modern-connect-mirror figure {
  margin: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: var(--modern-space-3);
}
.modern-connect-mirror svg {
  display: block;
  width: 100%;
  height: auto;
}
.modern-connect-mirror figcaption {
  margin-top: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  text-align: center;
}
.modern-mirror-frame {
  fill: var(--modern-surface);
  stroke: var(--modern-border);
}
.modern-mirror-line {
  stroke: var(--modern-border);
}
.modern-mirror-dot,
.modern-mirror-rail {
  fill: var(--modern-border);
}
.modern-mirror-label {
  fill: var(--modern-muted);
  font-size: var(--modern-mirror-label-size);
}
.modern-mirror-title {
  fill: var(--modern-muted);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-mirror-number-size);
}
.modern-mirror-field {
  fill: var(--modern-subtle);
  stroke: var(--modern-border);
}
.modern-mirror-field.is-marked {
  fill: color-mix(in srgb, var(--modern-accent) 8%, var(--modern-surface));
  stroke: var(--modern-accent);
}
.modern-mirror-marker {
  fill: var(--modern-accent);
}
.modern-mirror-number {
  fill: var(--modern-on-action);
  font-size: var(--modern-mirror-number-size);
  font-weight: var(--modern-weight-semibold);
  text-anchor: middle;
}
.modern-connect-guide {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-connect-guide > p {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-connect-values {
  display: grid;
  gap: var(--modern-space-2);
  margin: 0;
  padding: 0;
  min-width: 0;
  list-style: none;
}
.modern-connect-values > li {
  display: grid;
  grid-template-columns: var(--modern-space-5) minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-3) var(--modern-space-2) var(--modern-space-3) var(--modern-space-3);
}
.modern-connect-values > li > div {
  display: grid;
  gap: var(--modern-space-0-5);
  min-width: 0;
}
.modern-connect-number {
  display: grid;
  width: var(--modern-space-5);
  height: var(--modern-space-5);
  place-items: center;
  border-radius: var(--modern-radius-round);
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-connect-values h3 {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-medium);
}
.modern-connect-value {
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
}
@container modern-connect-body (max-width: 560px) {
  .modern-connect-mirror {
    grid-template-columns: minmax(0, 1fr);
  }
  .modern-connect-mirror figure {
    display: none;
  }
}
</style>
