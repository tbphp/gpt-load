<script setup lang="ts">
import http from "@/utils/http";
import { NAlert, NButton, NCollapse, NCollapseItem } from "naive-ui";
import { computed, onMounted, ref } from "vue";

// 加密状态响应接口
interface EncryptionStatusResponse {
  has_mismatch: boolean;
  message: string;
  suggestion: string;
}

// 是否显示警告
const showAlert = ref(false);

// 警告信息
const message = ref("");
const suggestion = ref("");

// 本次会话是否已关闭
const isClosedThisSession = ref(false);

// 是否显示详情
const showDetails = ref<string[]>([]);

// 是否应该显示
const shouldShow = computed(() => {
  return showAlert.value && !isClosedThisSession.value;
});

// 检查加密状态
const checkEncryptionStatus = async () => {
  try {
    const response = await http.get<EncryptionStatusResponse>("/dashboard/encryption-status");
    if (response.data.has_mismatch) {
      showAlert.value = true;
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
      <strong>⚠️ 加密配置错误</strong>
    </template>

    <div>
      <div style="margin-bottom: 16px; font-size: 14px; line-height: 1.5">
        {{ message }}
      </div>

      <n-collapse v-model:expanded-names="showDetails" style="margin-bottom: 12px">
        <n-collapse-item name="solution" title="查看解决方案">
          <div
            class="solution-content"
            style="padding: 16px; border-radius: 6px; font-size: 13px; line-height: 1.6"
          >
            <!-- 场景A: 已配置 ENCRYPTION_KEY 但数据未加密 -->
            <template v-if="message.includes('数据库中的密钥尚未加密')">
              <p style="margin: 0 0 8px 0">1. 停止服务</p>
              <p style="margin: 0 0 8px 0">2. 执行密钥迁移命令：</p>
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
              <p style="margin: 8px 0 0 0">3. 重启服务</p>
            </template>

            <!-- 场景C: 密钥不匹配 -->
            <template v-else-if="message.includes('密钥不匹配')">
              <div style="margin-bottom: 16px">
                <strong style="color: var(--primary-color)">方案一：使用正确的密钥（推荐）</strong>
                <p style="margin: 8px 0 4px 0">1. 在 .env 文件中配置正确的 ENCRYPTION_KEY：</p>
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
                <p style="margin: 4px 0">2. 重启服务</p>
              </div>

              <div>
                <strong style="color: var(--warning-color)">
                  方案二：重新加密数据（如果确定要使用新密钥）
                </strong>
                <p style="margin: 0 0 8px 0">1. 停止服务</p>
                <p style="margin: 4px 0">2. 执行密钥迁移到新密钥：</p>
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
                <p style="margin: 4px 0">3. 更新 .env 配置为新密钥</p>
                <p style="margin: 4px 0">4. 重启服务</p>
              </div>
            </template>

            <!-- 场景B: 数据已加密但未配置 ENCRYPTION_KEY -->
            <template v-else>
              <div style="margin-bottom: 16px">
                <strong style="color: var(--primary-color)">方案一：配置加密密钥（推荐）</strong>
                <p style="margin: 8px 0 4px 0">
                  1. 在 .env 文件中配置与加密时相同的 ENCRYPTION_KEY：
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
                <p style="margin: 4px 0">2. 重启服务</p>
              </div>

              <div>
                <strong style="color: var(--warning-color)">方案二：解密数据</strong>
                <p style="margin: 0 0 8px 0">1. 停止服务</p>
                <p style="margin: 4px 0">2. 执行解密命令：</p>
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
                <p style="margin: 4px 0">3. 重启服务</p>
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
        查看文档
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
