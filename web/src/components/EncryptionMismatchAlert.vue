<script setup lang="ts">
import http from "@/utils/http";
import { NAlert, NButton, NCollapse, NCollapseItem } from "naive-ui";
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";

// 加密状态响应接口
interface EncryptionStatusResponse {
  has_mismatch: boolean;
  scenario_type: string;
  message: string;
  suggestion: string;
}

// 是否显示警告
const showAlert = ref(false);

// 警告信息
const message = ref("");
const suggestion = ref("");
const scenarioType = ref("");

// 本次会话是否已关闭
const isClosedThisSession = ref(false);

// 是否显示详情
const showDetails = ref<string[]>([]);

// 是否应该显示
const shouldShow = computed(() => {
  return showAlert.value && !isClosedThisSession.value;
});

// i18n
const { t } = useI18n();

// 检查加密状态
const checkEncryptionStatus = async () => {
  try {
    const response = await http.get<EncryptionStatusResponse>("/dashboard/encryption-status");
    if (response.data.has_mismatch) {
      showAlert.value = true;
      scenarioType.value = response.data.scenario_type;
      message.value = response.data.message;
      suggestion.value = response.data.suggestion;
    }
  } catch (error) {
    console.error("Failed to check encryption status:", error);
  }
};

// 关闭警告（仅本次会话）
const handleClose = () => {
  isClosedThisSession.value = true;
};

// 打开文档
const openDocs = () => {
  window.open("https://www.gpt-load.com/docs/configuration/security", "_blank");
};

// 组件挂载时检查状态
onMounted(() => {
  checkEncryptionStatus();
});
</script>

<template>
  <n-alert
    v-if="shouldShow"
    type="error"
    :show-icon="false"
    closable
    @close="handleClose"
    style="margin-bottom: 16px"
  >
    <template #header>
      <strong>{{ t("encryptionAlert.title") }}</strong>
    </template>

    <div>
      <div style="margin-bottom: 16px; font-size: 14px; line-height: 1.5">
        {{ message }}
      </div>

      <n-collapse v-model:expanded-names="showDetails" style="margin-bottom: 12px">
        <n-collapse-item name="solution" :title="t('encryptionAlert.viewSolution')">
          <div
            class="solution-content"
            style="padding: 16px; border-radius: 6px; font-size: 13px; line-height: 1.6"
          >
            <!-- 场景A: 已配置 ENCRYPTION_KEY 但数据未加密 -->
            <template v-if="scenarioType === 'data_not_encrypted'">
              <p style="margin: 0 0 8px 0">
                1. {{ t("encryptionAlert.scenario.dataNotEncrypted.step1") }}
              </p>
              <p style="margin: 0 0 8px 0">
                2. {{ t("encryptionAlert.scenario.dataNotEncrypted.step2") }}
              </p>
              <pre
                style="
                  margin: 8px 0;
                  padding: 10px;
                  border-radius: 4px;
                  overflow-x: auto;
                  font-family: monospace;
                  font-size: 12px;
                "
              >
docker compose run --rm gpt-load migrate-keys --to "your-encryption-key"</pre
              >
              <p style="margin: 8px 0 0 0">
                3. {{ t("encryptionAlert.scenario.dataNotEncrypted.step3") }}
              </p>
            </template>

            <!-- 场景C: 密钥不匹配 -->
            <template v-else-if="scenarioType === 'key_mismatch'">
              <div style="margin-bottom: 16px">
                <strong style="color: var(--primary-color)">
                  {{ t("encryptionAlert.scenario.keyMismatch.solution1Title") }}
                </strong>
                <p style="margin: 8px 0 4px 0">
                  1. {{ t("encryptionAlert.scenario.keyMismatch.solution1Step1") }}
                </p>
                <pre
                  style="
                    margin: 4px 0 8px 0;
                    padding: 10px;
                    border-radius: 4px;
                    overflow-x: auto;
                    font-family: monospace;
                    font-size: 12px;
                  "
                >
ENCRYPTION_KEY=your-correct-encryption-key</pre
                >
                <p style="margin: 4px 0">
                  2. {{ t("encryptionAlert.scenario.keyMismatch.solution1Step2") }}
                </p>
              </div>

              <div>
                <strong style="color: var(--warning-color)">
                  {{ t("encryptionAlert.scenario.keyMismatch.solution2Title") }}
                </strong>
                <p style="margin: 0 0 8px 0">
                  1. {{ t("encryptionAlert.scenario.keyMismatch.solution2Step1") }}
                </p>
                <p style="margin: 4px 0">
                  2. {{ t("encryptionAlert.scenario.keyMismatch.solution2Step2") }}
                </p>
                <pre
                  style="
                    margin: 4px 0 8px 0;
                    padding: 10px;
                    border-radius: 4px;
                    overflow-x: auto;
                    font-family: monospace;
                    font-size: 12px;
                  "
                >
docker compose run --rm gpt-load migrate-keys --from "old-key" --to "new-key"</pre
                >
                <p style="margin: 4px 0">
                  3. {{ t("encryptionAlert.scenario.keyMismatch.solution2Step3") }}
                </p>
                <p style="margin: 4px 0">
                  4. {{ t("encryptionAlert.scenario.keyMismatch.solution2Step4") }}
                </p>
              </div>
            </template>

            <!-- 场景B: 数据已加密但未配置 ENCRYPTION_KEY -->
            <template v-else-if="scenarioType === 'key_not_configured'">
              <div style="margin-bottom: 16px">
                <strong style="color: var(--primary-color)">
                  {{ t("encryptionAlert.scenario.keyNotConfigured.solution1Title") }}
                </strong>
                <p style="margin: 8px 0 4px 0">
                  1. {{ t("encryptionAlert.scenario.keyNotConfigured.solution1Step1") }}
                </p>
                <pre
                  style="
                    margin: 4px 0 8px 0;
                    padding: 10px;
                    border-radius: 4px;
                    overflow-x: auto;
                    font-family: monospace;
                    font-size: 12px;
                  "
                >
ENCRYPTION_KEY=your-original-encryption-key</pre
                >
                <p style="margin: 4px 0">
                  2. {{ t("encryptionAlert.scenario.keyNotConfigured.solution1Step2") }}
                </p>
              </div>

              <div>
                <strong style="color: var(--warning-color)">
                  {{ t("encryptionAlert.scenario.keyNotConfigured.solution2Title") }}
                </strong>
                <p style="margin: 0 0 8px 0">
                  1. {{ t("encryptionAlert.scenario.keyNotConfigured.solution2Step1") }}
                </p>
                <p style="margin: 4px 0">
                  2. {{ t("encryptionAlert.scenario.keyNotConfigured.solution2Step2") }}
                </p>
                <pre
                  style="
                    margin: 4px 0 8px 0;
                    padding: 10px;
                    border-radius: 4px;
                    overflow-x: auto;
                    font-family: monospace;
                    font-size: 12px;
                  "
                >
docker compose run --rm gpt-load migrate-keys --from "old-key"</pre
                >
                <p style="margin: 4px 0">
                  3. {{ t("encryptionAlert.scenario.keyNotConfigured.solution2Step3") }}
                </p>
              </div>
            </template>
          </div>
        </n-collapse-item>
      </n-collapse>

      <n-button
        size="small"
        type="primary"
        :bordered="false"
        @click="openDocs"
        class="encryption-docs-btn"
      >
        {{ t("encryptionAlert.viewDocs") }}
      </n-button>
    </div>
  </n-alert>
</template>

<style scoped>
/* 解决方案内容背景 */
.solution-content {
  background: #f7f9fc;
  border: 1px solid #e1e4e8;
}

/* 浅色模式下的代码块 */
.solution-content pre {
  background: #f0f2f5;
  border: 1px solid #d6dae0;
}

/* 暗黑模式下的解决方案背景 */
:root.dark .solution-content {
  background: #1a1a1a;
  border: 1px solid rgba(255, 255, 255, 0.1);
}

/* 暗黑模式下的代码块 */
:root.dark .solution-content pre {
  background: #0d0d0d !important;
  border: 1px solid rgba(255, 255, 255, 0.08);
}

/* 按钮样式 */
.encryption-docs-btn {
  font-weight: 600;
}

/* 暗黑模式下的按钮优化 */
:root.dark .encryption-docs-btn {
  background: #d32f2f !important;
  color: white !important;
  border: none !important;
}

:root.dark .encryption-docs-btn:hover {
  background: #b71c1c !important;
  color: white !important;
}

/* 亮色模式下的按钮 */
:root:not(.dark) .encryption-docs-btn {
  background: #d32f2f !important;
  color: white !important;
  border: none !important;
}

:root:not(.dark) .encryption-docs-btn:hover {
  background: #b71c1c !important;
  color: white !important;
}
</style>
