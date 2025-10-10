<script setup lang="ts">
import { useAuthService } from "@/services/auth";
import { LogOutOutline } from "@vicons/ionicons5";
import { useRouter } from "vue-router";
import { useI18n } from "vue-i18n";

const { t } = useI18n();

const router = useRouter();
const { logout } = useAuthService();

const handleLogout = () => {
  logout();
  router.replace("/login");
};
</script>

<template>
  <n-button quaternary round class="logout-button" @click="handleLogout">
    <template #icon>
      <n-icon :component="LogOutOutline" />
    </template>
    {{ t("nav.logout") }}
  </n-button>
</template>

<style scoped>
.logout-button {
  color: var(--text-secondary);
  background: var(--card-bg);
  backdrop-filter: blur(8px);
  border: 1px solid var(--border-color-light);
  transition: all 0.2s ease;
  font-weight: 500;
  letter-spacing: 0.2px;
}

.logout-button:hover {
  color: #dc2626;
  background: rgba(239, 68, 68, 0.1);
  border-color: rgba(239, 68, 68, 0.2);
  transform: translateY(-1px);
  box-shadow: var(--shadow-md);
}

:deep(.n-button__content) {
  gap: 6px;
}
</style>
