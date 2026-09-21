<script setup lang="ts">
import { ChevronRight, PencilLine, Settings2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import {
  priceFields,
  type ModelSource,
  type PriceField,
  type RequestModel,
} from '@modern/api/models'
import {
  AppBadge,
  AppChannelIcon,
  AppCopyValue,
  AppIcon,
  AppIconButton,
  AppOverflowText,
  AppTooltip,
} from '@modern/components/ui'
import { protocolLabel } from '@modern/i18n/protocols'
import { modelGroupCount, modelUnitPrice, priceStatus } from './models-display'
import './model-group-chip.css'

const props = defineProps<{
  model: RequestModel
  admin: boolean
  selected?: boolean
  disabled?: boolean
}>()
const emit = defineEmits<{ open: [source?: number]; settings: [] }>()
const { t, n, locale } = useI18n()
// 协议在同一批模型上重复度接近 100%，折成一行中性摘要，悬浮再看全量列表。
const protocolsLabel = computed(() =>
  props.model.protocols.map((value) => protocolLabel(value, t)).join('\n'),
)
const groupCount = computed(() => modelGroupCount(props.model))
function price(source: ModelSource, field: PriceField): string {
  return modelUnitPrice(source.price.prices[field], locale.value)
}
// 表头与来源行是两个独立网格，用 max-content 会各算各的导致列错位。
// 这里按本卡最长的价格算出一个共享列宽，两边引用同一个值才能对齐。
// 不能用 ch：它相对元素自身字号，表头 11px 与数据行的字号会解析成两个不同的宽度。
// 0.63 是等宽数字单字符宽度相对字号的上界，乘固定字号 token 得到绝对宽度。
const priceWidth = computed(() => {
  const longest = props.model.sources.reduce(
    (width, source) =>
      priceFields.reduce((current, field) => Math.max(current, price(source, field).length), width),
    0,
  )
  return `calc(${longest} * 0.63 * var(--modern-font-size-small))`
})
// 只展开第一个分组：绝大多数来源只关联一个，展开两个会在窄列里换行、把行高撑乱。
function visibleGroups(source: ModelSource) {
  return source.groups.slice(0, 1)
}
function hiddenGroupsLabel(source: ModelSource): string {
  return source.groups
    .slice(1)
    .map((group) => group.name || t('logs.deleted'))
    .join('\n')
}
</script>

<template>
  <article class="modern-model-card" :class="{ 'is-selected': selected }" :aria-label="model.name">
    <header class="modern-model-card-heading">
      <span class="modern-model-card-name"><AppCopyValue :value="model.name" /></span>
      <AppBadge
        v-if="model.hasOverrides !== undefined"
        class="modern-model-card-profile"
        variant="outline"
        size="xs"
        :tone="model.hasOverrides ? 'brand' : 'neutral'"
        >{{
          t(
            model.hasOverrides
              ? 'modelManager.profile.badgeCustom'
              : 'modelManager.profile.badgeAutomatic',
          )
        }}</AppBadge
      >
      <AppTooltip :label="protocolsLabel">
        <span tabindex="0" class="modern-model-card-meta">{{
          t('modelManager.protocolCount', { count: n(model.protocols.length) })
        }}</span>
      </AppTooltip>
      <span class="modern-model-card-meta">{{
        t('modelManager.sourceCount', { count: n(model.sources.length) })
      }}</span>
      <span v-if="admin" class="modern-model-card-meta">{{
        t('modelManager.groupCount', { count: n(groupCount) })
      }}</span>
      <AppIconButton
        v-if="admin"
        :icon="Settings2"
        :label="t('modelManager.profile.action')"
        size="xxs"
        class="modern-model-card-profile-action"
        :disabled="disabled"
        @click="emit('settings')"
      />
    </header>
    <!-- 列宽在窄屏下有下限，超出卡片宽度时横向滚动而不是把标签挤到换行。 -->
    <div class="modern-model-card-table" :style="{ '--modern-model-price-width': priceWidth }">
      <div class="modern-model-row modern-model-row--head" aria-hidden="true">
        <span>{{ t('modelManager.sourceUnit') }}</span>
        <span>{{ t('modelManager.groupUnit') }}</span>
        <span class="r">{{ t('modelManager.slots.input') }}</span>
        <span class="r">{{ t('modelManager.slots.output') }}</span>
        <span class="r">{{ t('modelManager.columns.cacheRead') }}</span>
        <span class="r">{{ t('modelManager.columns.cacheWrite') }}</span>
        <span>{{ t('modelManager.columns.method') }}</span>
        <span></span>
      </div>
      <div class="modern-model-card-sources">
        <div v-for="source in model.sources" :key="source.price.id" class="modern-model-row">
          <span class="modern-model-source-name">
            <AppChannelIcon
              :icon="source.price.channel.icon"
              :mark="source.price.channel.mark"
              :name="source.price.channel.name"
              :tooltip="false"
              size="sm"
            />
            <span class="modern-model-source-channel">
              <AppOverflowText :text="source.price.channel.name || t('logs.deleted')" />
              <AppBadge
                v-if="source.price.context_tiers.length"
                variant="plain"
                size="xs"
                class="modern-model-source-tiers"
                >{{
                  t('modelManager.tierCount', { count: n(source.price.context_tiers.length) })
                }}</AppBadge
              >
            </span>
            <AppOverflowText
              v-if="source.model !== model.name"
              :text="source.model"
              class="modern-model-source-upstream"
            />
          </span>
          <span class="modern-model-source-groups">
            <template v-for="group in visibleGroups(source)" :key="group.id">
              <RouterLink
                v-if="admin"
                :to="{ name: 'modern-group-detail', params: { id: group.id } }"
                class="modern-model-group-chip"
                :class="{ 'is-disabled': !group.enabled }"
                ><AppOverflowText :text="group.name || t('logs.deleted')"
              /></RouterLink>
              <span
                v-else
                class="modern-model-group-chip"
                :class="{ 'is-disabled': !group.enabled }"
                ><AppOverflowText :text="group.name || t('logs.deleted')"
              /></span>
            </template>
            <AppTooltip v-if="source.groups.length > 1" :label="hiddenGroupsLabel(source)">
              <span tabindex="0" class="modern-model-source-more"
                >+{{ n(source.groups.length - 1) }}</span
              >
            </AppTooltip>
          </span>
          <span
            v-for="field in priceFields"
            :key="field"
            class="r modern-model-source-price"
            :class="{ 'is-empty': source.price.prices[field] === null }"
            >{{ price(source, field) }}</span
          >
          <span class="modern-model-source-method" :class="'is-' + priceStatus(source.price)">
            <AppBadge v-if="priceStatus(source.price) === 'pending'" tone="warning" size="xs">{{
              t('modelManager.priceMethods.pending')
            }}</AppBadge>
            <template v-else>
              <AppIcon v-if="priceStatus(source.price) === 'manual'" :icon="PencilLine" size="xs" />
              <AppOverflowText
                :text="t('modelManager.priceMethods.' + priceStatus(source.price))"
              />
            </template>
          </span>
          <AppIconButton
            :icon="ChevronRight"
            :label="t('modelManager.details')"
            size="xs"
            :disabled="disabled"
            @click="emit('open', source.price.id)"
          />
        </div>
      </div>
    </div>
  </article>
</template>

<style scoped>
.modern-model-card {
  display: flex;
  min-width: 0;
  flex-direction: column;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  transition:
    border-color var(--modern-motion-fast) var(--modern-motion-ease),
    box-shadow var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-model-card:hover {
  border-color: var(--modern-control-border-hover);
}
.modern-model-card.is-selected {
  border-color: var(--modern-segmented-active-border);
  box-shadow: var(--modern-shadow-control);
}
/* 自右上角发散的椭圆晕染，向左下淡出——与用量统计顶部卡片同一套手法。
   线性渐变在 15:1 的扁盒子里只会变成一条生硬的色带。 */
.modern-model-card-heading {
  position: relative;
  isolation: isolate;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--modern-space-1) var(--modern-space-2);
  min-width: 0;
  overflow: hidden;
  border-radius: var(--modern-radius-panel) var(--modern-radius-panel) 0 0;
  padding: var(--modern-space-3) var(--modern-space-4);
}
.modern-model-card-heading::before {
  position: absolute;
  z-index: var(--modern-layer-underlay);
  inset: 0;
  background: radial-gradient(
    ellipse at top right,
    color-mix(in srgb, var(--modern-model-card-tint) 14%, var(--modern-surface)),
    transparent 72%
  );
  content: '';
  pointer-events: none;
}
/* 把后面三项统计推到右侧，模型名独占左端。 */
.modern-model-card-name {
  margin-inline-end: auto;
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  line-height: var(--modern-leading-compact);
}
.modern-model-card-meta {
  flex: none;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  letter-spacing: var(--modern-tracking-label);
}
.modern-model-card-profile,
.modern-model-card-profile-action {
  flex: none;
  align-self: center;
}
/* 摘要读成一句而不是三个孤立标签。 */
.modern-model-card-meta + .modern-model-card-meta::before {
  content: '·';
  margin-inline-end: var(--modern-space-2);
  color: var(--modern-control-placeholder);
}
/* 列宽有下限，卡片窄于内容时整体横向滚动，而不是挤压换行。 */
.modern-model-card-table {
  /* 实际值由模板按本卡最长价格注入，这里只提供兜底。 */
  --modern-model-price-width: 0px;
  overflow-x: auto;
}
/* 表头与来源行共用同一套列定义：来源 / 分组 / 4 个价格 / 计价方式 / 操作，
   多来源时价格天然对齐可比，这是这页配价工作的核心诉求。 */
.modern-model-row {
  display: grid;
  /* 价格单位是「美元 / 百万 Tokens」，可能出现 9 位小数，写死宽度会让数字溢出压到
     隔壁列。列宽取本卡最长价格（见 priceWidth），超出卡宽时交给外层横向滚动。 */
  grid-template-columns:
    minmax(140px, 1.4fr) 88px repeat(4, max(56px, var(--modern-model-price-width))) 72px
    var(--modern-control-xs);
  align-items: center;
  gap: var(--modern-space-1-5);
  /* 除首列外所有轨道都是定值，min-content 即「轨道最小总和」：
     横向滚动时行的底色与分隔线才会延伸到完整宽度，而不是在容器右边缘断掉。 */
  min-width: min-content;
  padding-inline: var(--modern-space-4);
}
.modern-model-row--head {
  min-height: var(--modern-space-6);
  border-block: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-subtle);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  letter-spacing: var(--modern-tracking-label);
}
.modern-model-card-sources > .modern-model-row {
  min-height: var(--modern-space-10);
}
.modern-model-card-sources > .modern-model-row + .modern-model-row {
  border-top: var(--modern-line-width) solid
    color-mix(in srgb, var(--modern-border) 55%, transparent);
}
.modern-model-row .r {
  overflow: hidden;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 用网格而不是 flex-wrap：渠道名一长，wrap 会把图标、名称、档位各自甩到新行，
   单元格从 24px 涨到 87px。这里图标恒定在第一列，文字只在第二列内部换行。 */
.modern-model-source-name {
  display: grid;
  grid-template-columns: var(--modern-channel-sm) minmax(0, 1fr);
  align-items: center;
  column-gap: var(--modern-space-1-5);
  min-width: 0;
  font-size: var(--modern-font-size-small);
}
.modern-model-source-channel {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1-5);
  min-width: 0;
}
.modern-model-source-tiers {
  flex: none;
  color: var(--modern-control-placeholder);
}
/* 两行内容合计 39.4px，压在行的 40px 下限内，有无上游模型的行才能等高。 */
.modern-model-source-upstream {
  grid-column: 2;
  color: var(--modern-muted);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-caption);
  line-height: var(--modern-leading-compact);
}
/* 不换行：分组一多就折行会把行高撑乱，超出的交给 +N 提示。 */
.modern-model-source-groups {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  min-width: 0;
}
.modern-model-source-more {
  flex: none;
  color: var(--modern-control-placeholder);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
.modern-model-source-price {
  min-width: 0;
  color: var(--modern-text);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.modern-model-source-price.is-empty {
  color: var(--modern-control-placeholder);
}
/* 待计价是徽章、其余是纯文字，给统一高度免得这一列的基线上下跳。 */
.modern-model-source-method {
  display: flex;
  min-width: 0;
  min-height: var(--modern-badge-xs);
  align-items: center;
  gap: var(--modern-space-1);
  color: var(--modern-control-placeholder);
  font-size: var(--modern-font-size-caption);
}
.modern-model-source-method.is-manual {
  color: var(--modern-muted);
}
.modern-model-source-method.is-unpriced {
  text-decoration: line-through;
}
</style>
