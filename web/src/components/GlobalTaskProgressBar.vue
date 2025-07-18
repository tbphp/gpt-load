<script setup lang="ts">
import { keysApi } from "@/api/keys";
import type { TaskInfo } from "@/types/models";
import { appState } from "@/utils/app-state";
import { NButton, NCard, NProgress, NText, useMessage } from "naive-ui";
import { onBeforeUnmount, onMounted, ref, watch } from "vue";

const taskInfo = ref<TaskInfo>({ is_running: false });
const visible = ref(false);
let pollTimer: number | null = null;
let isPolling = false; // Add flag
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
          const { total_keys, valid_keys, invalid_keys } = task.result;
          const msg = `Task completed, processed ${total_keys} keys, including ${valid_keys} valid keys and ${invalid_keys} invalid keys.`;
          message.info(msg, {
            closable: true,
            duration: 0,
            onClose: () => {
              localStorage.setItem("last_closed_task", task.finished_at || "");
            },
          });
        }
      }
      return;
    }
  } catch (_error) {
    // Error already logged
  }

  // If still polling, make next request after 1 second
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
</script>

<template>
  <n-card v-if="visible" class="global-task-progress" :bordered="false" size="small">
    <div class="progress-container">
      <div class="progress-header">
        <div class="progress-info">
          <span class="progress-icon">⚡</span>
          <div class="progress-details">
            <n-text strong class="progress-title">
              Processing task for group {{ taskInfo.group_name }}
            </n-text>
            <n-text depth="3" class="progress-subtitle">
              {{ getProgressText() }} ({{ getProgressPercentage() }}%)
            </n-text>
          </div>
        </div>
        <n-button quaternary circle size="small" @click="handleClose" title="Hide progress bar">
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
  width: 350px;
  background: white;
  border-radius: var(--border-radius-md);
  box-shadow: var(--shadow-lg);
  border: 1px solid rgba(0, 0, 0, 0.08);
  animation: slideIn 0.3s ease-out;
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
