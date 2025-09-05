<script setup lang="ts">
import { themeMode, toggleTheme } from "@/utils/theme";
import { Contrast, Moon, Sunny } from "@vicons/ionicons5";
import { NButton, NIcon, NTooltip } from "naive-ui";
import { computed } from "vue";

// 根据当前主题模式计算图标和提示文字
const themeConfig = computed(() => {
  switch (themeMode.value) {
    case "auto":
      return {
        icon: Contrast,
        tooltip: "自动模式",
        nextMode: "浅色模式",
      };
    case "light":
      return {
        icon: Sunny,
        tooltip: "浅色模式",
        nextMode: "深色模式",
      };
    case "dark":
      return {
        icon: Moon,
        tooltip: "深色模式",
        nextMode: "自动模式",
      };
    default:
      return {
        icon: Contrast,
        tooltip: "自动模式",
        nextMode: "浅色模式",
      };
  }
});
</script>

<template>
  <n-tooltip trigger="hover">
    <template #trigger>
      <n-button quaternary circle @click="toggleTheme">
        <template #icon>
          <n-icon :component="themeConfig.icon" />
        </template>
      </n-button>
    </template>
    <div>
      <div>当前：{{ themeConfig.tooltip }}</div>
      <div style="font-size: 12px; opacity: 0.8">点击切换到{{ themeConfig.nextMode }}</div>
    </div>
  </n-tooltip>
</template>
