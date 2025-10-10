<script setup lang="ts">
import { themeMode, toggleTheme } from "@/utils/theme";
import { Contrast, Moon, Sunny } from "@vicons/ionicons5";
import { NButton, NIcon, NTooltip } from "naive-ui";
import { computed } from "vue";
import { useI18n } from "vue-i18n";

const { t } = useI18n();

// 根据当前主题模式计算图标和提示文字
const themeConfig = computed(() => {
  switch (themeMode.value) {
    case "auto":
      return {
        icon: Contrast,
        tooltip: t("theme.auto"),
        nextMode: t("theme.light"),
      };
    case "light":
      return {
        icon: Sunny,
        tooltip: t("theme.light"),
        nextMode: t("theme.dark"),
      };
    case "dark":
      return {
        icon: Moon,
        tooltip: t("theme.dark"),
        nextMode: t("theme.auto"),
      };
    default:
      return {
        icon: Contrast,
        tooltip: t("theme.auto"),
        nextMode: t("theme.light"),
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
      <div>{{ t("theme.current") }}: {{ themeConfig.tooltip }}</div>
      <div style="font-size: 12px; opacity: 0.8">
        {{ t("theme.clickToSwitch", { mode: themeConfig.nextMode }) }}
      </div>
    </div>
  </n-tooltip>
</template>
