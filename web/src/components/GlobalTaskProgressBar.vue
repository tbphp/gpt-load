<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { TaskInfo } from "@/types/models";
import { appState } from "@/utils/app-state";
import { NButton, NCard, NProgress, NText, useMessage } from "naive-ui";
import { onBeforeUnmount, onMounted, ref, watch } from "vue";

const taskInfo = ref<TaskInfo>({ is_running: false, task_type: "KEY_VALIDATION" });
const visible = ref(false);
let pollTimer: number | null = null;
let isPolling = false; // 添加标志位
const message = useMessage();

onMounted(() => {
  startPolling();
});

watch(
  () => appState.taskPollingTrigger,
  () => {
    startPolling();
  }
);

onBeforeUnmount(() => {
  stopPolling();
});

function startPolling() {
  stopPolling();
  isPolling = true;
  pollOnce();
}

async function pollOnce() {
  if (!isPolling) {
    return;
  }

  try {
    const task = await keysApi.getTaskStatus();
    taskInfo.value = task;
    visible.value = task.is_running;
    if (!task.is_running) {
      stopPolling();
      if (task.result) {
        const lastTask = localStorage.getItem("last_closed_task");
        if (lastTask !== task.finished_at) {
          let msg = "任务已完成。";
          if (task.task_type === "KEY_VALIDATION") {
            const result = task.result as import("@/types/models").KeyValidationResult;
            msg = `密钥验证完成，处理了 ${result.total_keys} 个密钥，其中 ${result.valid_keys} 个成功，${result.invalid_keys} 个失败。请注意：验证失败并不一定拉黑该密钥，需要失败次数达到阈值才会拉黑。`;
          } else if (task.task_type === "KEY_IMPORT") {
            const result = task.result as import("@/types/models").KeyImportResult;
            msg = `密钥导入完成，成功添加 ${result.added_count} 个密钥，忽略了 ${result.ignored_count} 个。`;
          } else if (task.task_type === "KEY_DELETE") {
            const result = task.result as import("@/types/models").KeyDeleteResult;
            msg = `密钥删除完成，成功删除 ${result.deleted_count} 个密钥，忽略了 ${result.ignored_count} 个。`;
          }

          message.info(msg, {
            closable: true,
            duration: 0,
            onClose: () => {
              localStorage.setItem("last_closed_task", task.finished_at || "");
            },
          });

          // 触发分组数据刷新
          if (task.group_name && task.finished_at) {
            appState.lastCompletedTask = {
              groupName: task.group_name,
              taskType: task.task_type,
              finishedAt: task.finished_at,
            };
            appState.groupDataRefreshTrigger++;
          }
        }
      }
      return;
    }
  } catch (_error) {
    // 错误已记录
  }

  // 如果仍在轮询状态，1秒后发起下一次请求
  if (isPolling) {
    pollTimer = setTimeout(pollOnce, 1000);
  }
}

function stopPolling() {
  isPolling = false;
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function getProgressPercentage(): number {
  if (!taskInfo.value.total || taskInfo.value.total === 0) {
    return 0;
  }
  return Math.round(((taskInfo.value.processed || 0) / taskInfo.value.total) * 100);
}

function getProgressText(): string {
  const { processed = 0, total = 0 } = taskInfo.value;
  return `${processed}/${total}`;
}

function handleClose() {
  visible.value = false;
}

function getTaskTitle(): string {
  if (!taskInfo.value) {
    return "正在处理任务...";
  }
  switch (taskInfo.value.task_type) {
    case "KEY_VALIDATION":
      return `正在验证分组 [${taskInfo.value.group_name}] 的密钥`;
    case "KEY_IMPORT":
      return `正在向分组 [${taskInfo.value.group_name}] 导入密钥`;
    case "KEY_DELETE":
      return `正在删除分组 [${taskInfo.value.group_name}] 的密钥`;
    default:
      return "正在处理任务...";
  }
}
</script>

<template>
  <n-card v-if="visible" class="global-task-progress" :bordered="false" size="small">
    <div class="progress-container">
      <div class="progress-header">
        <div class="progress-info">
          <span class="progress-icon">⚡</span>
          <div class="progress-details">
            <n-text strong class="progress-title">
              {{ getTaskTitle() }}
            </n-text>
            <n-text depth="3" class="progress-subtitle">
              {{ getProgressText() }} ({{ getProgressPercentage() }}%)
            </n-text>
          </div>
        </div>
        <n-button quaternary circle size="small" @click="handleClose" title="隐藏进度条">
          <template #icon>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path
                d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"
              />
            </svg>
          </template>
        </n-button>
      </div>

      <n-progress
        :percentage="getProgressPercentage()"
        :show-indicator="false"
        processing
        type="line"
        :height="6"
        border-radius="3px"
        class="progress-bar"
      />
    </div>
  </n-card>
</template>

<style scoped>
.global-task-progress {
  position: fixed;
  bottom: 62px;
  right: 10px;
  z-index: 9999;
  width: 95%;
  max-width: 350px;
  background: white;
  border-radius: var(--border-radius-md);
  box-shadow: var(--shadow-lg);
  border: 1px solid rgba(0, 0, 0, 0.08);
  animation: slideIn 0.3s ease-out;
}

@media (max-width: 768px) {
  .global-task-progress {
    bottom: 72px;
    left: 50%;
    transform: translateX(-50%);
  }
}

@keyframes slideIn {
  from {
    transform: translateX(100%);
    opacity: 0;
  }
  to {
    transform: translateX(0);
    opacity: 1;
  }
}

.progress-container {
  padding: 4px 0;
}

.progress-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.progress-info {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
}

.progress-icon {
  font-size: 20px;
  animation: pulse 1.5s ease-in-out infinite;
}

@keyframes pulse {
  0%,
  100% {
    transform: scale(1);
  }
  50% {
    transform: scale(1.1);
  }
}

.progress-details {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.progress-title {
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 2px;
}

.progress-subtitle {
  font-size: 12px;
}

.progress-bar {
  margin-bottom: 8px;
}

.progress-message {
  font-size: 12px;
  text-align: center;
  padding: 8px;
  background: rgba(102, 126, 234, 0.05);
  border-radius: var(--border-radius-sm);
  margin-top: 8px;
}
</style>
