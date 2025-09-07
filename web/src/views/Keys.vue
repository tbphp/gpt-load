<script setup lang="ts">
import { keysApi } from "@/api/keys";
import EncryptionMismatchAlert from "@/components/EncryptionMismatchAlert.vue";
import GroupInfoCard from "@/components/keys/GroupInfoCard.vue";
import GroupList from "@/components/keys/GroupList.vue";
import KeyTable from "@/components/keys/KeyTable.vue";
import type { Group } from "@/types/models";
import { onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";

const groups = ref<Group[]>([]);
const loading = ref(false);
const selectedGroup = ref<Group | null>(null);
const router = useRouter();
const route = useRoute();

onMounted(async () => {
  await loadGroups();
});

async function loadGroups() {
  try {
    loading.value = true;
    groups.value = await keysApi.getGroups();
    // 选择默认分组
    if (groups.value.length > 0 && !selectedGroup.value) {
      const groupId = route.query.groupId;
      const found = groups.value.find(g => String(g.id) === String(groupId));
      if (found) {
        selectedGroup.value = found;
      } else {
        handleGroupSelect(groups.value[0]);
      }
    }
  } finally {
    loading.value = false;
  }
}

function handleGroupSelect(group: Group | null) {
  selectedGroup.value = group || null;
  if (String(group?.id) !== String(route.query.groupId)) {
    router.push({ name: "keys", query: { groupId: group?.id || "" } });
  }
}

async function handleGroupRefresh() {
  await loadGroups();
  if (selectedGroup.value) {
    // 重新加载当前选中的分组信息
    handleGroupSelect(groups.value.find(g => g.id === selectedGroup.value?.id) || null);
  }
}

async function handleGroupRefreshAndSelect(targetGroupId: number) {
  await loadGroups();
  // 刷新完成后，切换到指定的分组
  const targetGroup = groups.value.find(g => g.id === targetGroupId);
  if (targetGroup) {
    handleGroupSelect(targetGroup);
  }
}

function handleGroupDelete(deletedGroup: Group) {
  // 从分组列表中移除已删除的分组
  groups.value = groups.value.filter(g => g.id !== deletedGroup.id);

  // 如果删除的是当前选中的分组，则切换到第一个分组
  if (selectedGroup.value?.id === deletedGroup.id) {
    handleGroupSelect(groups.value.length > 0 ? groups.value[0] : null);
  }
}

async function handleGroupCopySuccess(newGroup: Group) {
  // 重新加载分组列表以包含新创建的分组
  await loadGroups();
  // 自动切换到新创建的分组
  const createdGroup = groups.value.find(g => g.id === newGroup.id);
  if (createdGroup) {
    handleGroupSelect(createdGroup);
  }
}
</script>

<template>
  <div>
    <!-- 加密配置错误警告 -->
    <encryption-mismatch-alert style="margin-bottom: 16px" />

    <div class="keys-container">
      <div class="sidebar">
        <group-list
          :groups="groups"
          :selected-group="selectedGroup"
          :loading="loading"
          @group-select="handleGroupSelect"
          @refresh="handleGroupRefresh"
          @refresh-and-select="handleGroupRefreshAndSelect"
        />
      </div>

      <!-- 右侧主内容区域，占80% -->
      <div class="main-content">
        <!-- 分组信息卡片，更紧凑 -->
        <div class="group-info">
          <group-info-card
            :group="selectedGroup"
            @refresh="handleGroupRefresh"
            @delete="handleGroupDelete"
            @copy-success="handleGroupCopySuccess"
          />
        </div>

        <!-- 密钥表格区域，占主要空间 -->
        <div class="key-table-section">
          <key-table :selected-group="selectedGroup" />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.keys-container {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.sidebar {
  width: 100%;
  flex-shrink: 0;
}

.main-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.group-info {
  flex-shrink: 0;
}

.key-table-section {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

@media (min-width: 768px) {
  .keys-container {
    flex-direction: row;
  }

  .sidebar {
    width: 240px;
    height: calc(100vh - 159px);
  }
}
</style>
