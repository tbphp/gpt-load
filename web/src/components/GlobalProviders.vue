<script setup lang="ts">
import { appState } from "@/utils/app-state";
import {
  NConfigProvider,
  NDialogProvider,
  NLoadingBarProvider,
  NMessageProvider,
  useLoadingBar,
  useMessage,
  type GlobalThemeOverrides,
} from "naive-ui";
import { defineComponent, watch } from "vue";

// 自定义主题配置
const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: "#667eea",
    primaryColorHover: "#5a6fd8",
    primaryColorPressed: "#4c63d2",
    primaryColorSuppl: "#8b9df5",
    borderRadius: "12px",
    borderRadiusSmall: "8px",
    fontFamily: "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
  },
  Card: {
    paddingMedium: "24px",
  },
  Button: {
    fontWeight: "600",
    heightMedium: "40px",
    heightLarge: "48px",
  },
  Input: {
    heightMedium: "40px",
    heightLarge: "48px",
  },
  Menu: {
    itemHeight: "42px",
  },
};

function useGlobalMessage() {
  window.$message = useMessage();
}

const LoadingBar = defineComponent({
  setup() {
    const loadingBar = useLoadingBar();
    watch(
      () => appState.loading,
      loading => {
        if (loading) {
          loadingBar.start();
        } else {
          loadingBar.finish();
        }
      }
    );
    return () => null;
  },
});

const Message = defineComponent({
  setup() {
    useGlobalMessage();
    return () => null;
  },
});
</script>

<template>
  <n-config-provider :theme-overrides="themeOverrides">
    <n-loading-bar-provider>
      <n-message-provider placement="top-right">
        <n-dialog-provider>
          <slot />
          <loading-bar />
          <message />
        </n-dialog-provider>
      </n-message-provider>
    </n-loading-bar-provider>
  </n-config-provider>
</template>
