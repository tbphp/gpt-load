<script setup lang="ts">
defineProps<{
  title: string
  id?: string
}>()
</script>

<template>
  <header class="home-section-heading">
    <h2 :id="id">{{ title }}</h2>
    <div v-if="$slots.actions" class="home-section-heading__actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
/*
 * 与监控页分区标题同款：圆点 + 加粗。原来的「淡色图标 + serif 500」比板块
 * 内容还轻，扫视时抓不住锚点，这里把标题的视觉重量补回来。
 */
.home-section-heading {
  display: flex;
  min-width: 0;
  min-height: 26px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.home-section-heading h2 {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2-5);
  margin: 0;
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
  line-height: var(--line-compact);
}

.home-section-heading h2::before {
  flex: 0 0 auto;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-action);
  content: '';
}

.home-section-heading__actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: var(--space-2);
}
</style>
