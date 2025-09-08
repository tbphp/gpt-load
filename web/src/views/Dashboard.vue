<script setup lang="ts">
import { getDashboardStats } from "@/api/dashboard";
import BaseInfoCard from "@/components/BaseInfoCard.vue";
import EncryptionMismatchAlert from "@/components/EncryptionMismatchAlert.vue";
import LineChart from "@/components/LineChart.vue";
import SecurityAlert from "@/components/SecurityAlert.vue";
import type { DashboardStatsResponse } from "@/types/models";
import { NSpace } from "naive-ui";
import { onMounted, ref } from "vue";

const dashboardStats = ref<DashboardStatsResponse | null>(null);

onMounted(async () => {
  try {
    const response = await getDashboardStats();
    dashboardStats.value = response.data;
  } catch (error) {
    console.error("Failed to load dashboard stats:", error);
  }
});
</script>

<template>
  <div class="dashboard-container">
    <n-space vertical size="large">
      <!-- 加密配置错误警告（优先级最高） -->
      <encryption-mismatch-alert />

      <!-- 安全警告横幅 -->
      <security-alert
        v-if="dashboardStats?.security_warnings"
        :warnings="dashboardStats.security_warnings"
      />

      <base-info-card :stats="dashboardStats" />
      <line-chart class="dashboard-chart" />
    </n-space>
  </div>
</template>

<style scoped>
.dashboard-header-card {
  background: var(--card-bg);
  border-radius: var(--border-radius-lg);
  border: 1px solid var(--border-color-light);
  animation: fadeInUp 0.2s ease-out;
}

.dashboard-title {
  font-size: 2.25rem;
  font-weight: 700;
  margin: 0;
  letter-spacing: -0.5px;
}

.dashboard-subtitle {
  font-size: 1.1rem;
  font-weight: 500;
}

.dashboard-chart {
  animation: fadeInUp 0.2s ease-out 0.2s both;
}

@keyframes fadeInUp {
  from {
    opacity: 0;
    transform: translateY(20px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
</style>
