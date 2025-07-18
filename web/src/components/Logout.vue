<script setup lang="ts">
import { useAuthService } from "@/services/auth";
import { LogOutOutline } from "@vicons/ionicons5";
import { useRouter } from "vue-router";

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
    Logout
  </n-button>
</template>

<style scoped>
.logout-button {
  color: #64748b;
  background: rgba(255, 255, 255, 0.8);
  backdrop-filter: blur(8px);
  border: 1px solid rgba(0, 0, 0, 0.08);
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
