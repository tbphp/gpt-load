<script setup lang="ts">
import type { SecurityWarning } from "@/types/models";
import {
  NAlert,
  NButton,
  NCollapse,
  NCollapseItem,
  NList,
  NListItem,
  NSpace,
  NTag,
} from "naive-ui";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";

const { t } = useI18n();

interface Props {
  warnings: SecurityWarning[];
}

const props = defineProps<Props>();

// 本地存储键名
const STORAGE_KEY = "security-alert-dismissed";

// 检查是否已经被用户设置为不再提醒
const isDismissedPermanently = ref(localStorage.getItem(STORAGE_KEY) === "true");

// 本次会话是否已关闭
const isClosedThisSession = ref(false);

// 是否显示详情
const showDetails = ref<string[]>([]);

// 是否显示警告
const shouldShow = computed(() => {
  return props.warnings.length > 0 && !isDismissedPermanently.value && !isClosedThisSession.value;
});

// 获取最高严重级别
const highestSeverity = computed(() => {
  if (!props.warnings.length) {
    return "low";
  }

  const severityOrder = { high: 3, medium: 2, low: 1 };
  return props.warnings.reduce((highest, warning) => {
    const currentLevel = severityOrder[warning.severity as keyof typeof severityOrder] || 1;
    const highestLevel = severityOrder[highest as keyof typeof severityOrder] || 1;
    return currentLevel > highestLevel ? warning.severity : highest;
  }, "low");
});

// 获取警告类型的映射（调整为更温和的颜色）
const alertType = computed(() => {
  switch (highestSeverity.value) {
    case "high":
      return "warning"; // 使用橙色而非红色
    case "medium":
      return "info"; // 使用蓝色
    default:
      return "info";
  }
});

// 生成警告摘要文本
const warningText = computed(() => {
  const count = props.warnings.length;
  const highCount = props.warnings.filter(w => w.severity === "high").length;

  if (highCount > 0) {
    return t("security.warningsWithHigh", { count, highCount });
  } else {
    return t("security.warningsSuggestions", { count });
  }
});

// 获取严重程度标签类型
const getSeverityTagType = (severity: string) => {
  switch (severity) {
    case "high":
      return "error";
    case "medium":
      return "warning";
    default:
      return "info";
  }
};

// 获取严重程度中文
const getSeverityText = (severity: string) => {
  switch (severity) {
    case "high":
      return t("security.important");
    case "medium":
      return t("security.suggestion");
    default:
      return t("security.tip");
  }
};

// 关闭警告（仅本次会话）
const handleClose = () => {
  isClosedThisSession.value = true;
};

// 不再提醒
const handleDismissPermanently = () => {
  localStorage.setItem(STORAGE_KEY, "true");
  isDismissedPermanently.value = true;
};

// 打开安全配置文档
const openSecurityDocs = () => {
  window.open("https://www.gpt-load.com/docs/configuration/security", "_blank");
};
</script>

<template>
  <n-alert
    v-if="shouldShow"
    :type="alertType"
    :show-icon="false"
    closable
    @close="handleClose"
    style="margin-bottom: 16px"
  >
    <template #header>
      <strong>{{ t("security.configReminder") }}</strong>
    </template>

    <div>
      <div style="margin-bottom: 16px; font-size: 14px">
        {{ warningText }}
      </div>

      <!-- 问题详情列表 -->
      <n-collapse v-model:expanded-names="showDetails" style="margin-bottom: 12px">
        <n-collapse-item name="details" :title="t('security.viewDetails')">
          <n-list style="padding-top: 8px; margin-left: 0">
            <n-list-item
              v-for="(warning, index) in warnings"
              :key="index"
              style="padding: 12px 16px; border-bottom: 1px solid var(--border-color)"
            >
              <template #prefix>
                <n-tag
                  :type="getSeverityTagType(warning.severity)"
                  size="small"
                  style="margin-right: 12px; min-width: 40px; text-align: center"
                >
                  {{ getSeverityText(warning.severity) }}
                </n-tag>
              </template>

              <div style="flex: 1">
                <div
                  style="
                    font-weight: 500;
                    color: var(--text-primary);
                    margin-bottom: 6px;
                    font-size: 14px;
                  "
                >
                  {{ warning.message }}
                </div>
                <div style="font-size: 12px; color: var(--text-secondary); line-height: 1.4">
                  {{ warning.suggestion }}
                </div>
              </div>
            </n-list-item>
          </n-list>
        </n-collapse-item>
      </n-collapse>

      <n-space size="small">
        <n-button
          size="small"
          type="primary"
          @click="openSecurityDocs"
          class="security-primary-btn"
        >
          {{ t("security.configDocs") }}
        </n-button>

        <n-button
          size="small"
          secondary
          @click="handleDismissPermanently"
          class="security-secondary-btn"
        >
          {{ t("security.dontRemind") }}
        </n-button>
      </n-space>
    </div>
  </n-alert>
</template>

<style scoped>
/* 安全提醒按钮样式优化 */
.security-primary-btn {
  font-weight: 600;
}

.security-secondary-btn {
  font-weight: 500;
}

/* 暗黑模式下的按钮优化 */
:root.dark .security-primary-btn {
  background: var(--primary-color) !important;
  color: white !important;
  border: 1px solid var(--primary-color) !important;
}

:root.dark .security-primary-btn:hover {
  background: var(--primary-color-hover) !important;
  border-color: var(--primary-color-hover) !important;
}

:root.dark .security-secondary-btn {
  background: rgba(255, 255, 255, 0.1) !important;
  color: var(--text-primary) !important;
  border: 1px solid rgba(255, 255, 255, 0.2) !important;
}

:root.dark .security-secondary-btn:hover {
  background: rgba(255, 255, 255, 0.15) !important;
  border-color: rgba(255, 255, 255, 0.3) !important;
}
</style>
