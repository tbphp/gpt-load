<script setup lang="ts">
import { keysApi } from "@/api/keys";
import EncryptionMismatchAlert from "@/components/EncryptionMismatchAlert.vue";
import GroupInfoCard from "@/components/keys/GroupInfoCard.vue";
import GroupList from "@/components/keys/GroupList.vue";
import KeyTable from "@/components/keys/KeyTable.vue";
import SubGroupTable from "@/components/keys/SubGroupTable.vue";
import type { Group, SubGroupInfo } from "@/types/models";
import { onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const groups = ref<Group[]>([]);
const loading = ref(false);
const selectedGroup = ref<Group | null>(null);
const subGroups = ref<SubGroupInfo[]>([]);
const loadingSubGroups = ref(false);
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
  } catch (error) {
    console.error("Failed to load groups:", error);
    window.$message?.error("加载分组列表失败");
  } finally {
    loading.value = false;
  }
}

async function loadSubGroups() {
  if (!selectedGroup.value?.id || selectedGroup.value.group_type !== "aggregate") {
    subGroups.value = [];
    return;
  }

  try {
    loadingSubGroups.value = true;
    subGroups.value = await keysApi.getSubGroups(selectedGroup.value.id);
  } catch (error) {
    console.error("Failed to load sub groups:", error);
    window.$message?.error("加载子分组失败");
    subGroups.value = [];
  } finally {
    loadingSubGroups.value = false;
  }
}

// 监听选中分组变化，加载子分组数据
watch(selectedGroup, async newGroup => {
  if (newGroup?.group_type === "aggregate") {
    await loadSubGroups();
  } else {
    subGroups.value = [];
  }
});

function handleGroupSelect(group: Group | null) {
  selectedGroup.value = group || null;
  if (String(group?.id) !== String(route.query.groupId)) {
    router.push({ name: "keys", query: { groupId: group?.id || "" } });
  }
}

async function refreshGroupsAndSelect(targetGroupId?: number, selectFirst = true) {
  await loadGroups();

  if (targetGroupId) {
    const targetGroup = groups.value.find(g => g.id === targetGroupId);
    if (targetGroup) {
      handleGroupSelect(targetGroup);
      return;
    }
  }

  if (selectedGroup.value) {
    const currentGroup = groups.value.find(g => g.id === selectedGroup.value?.id);
    if (currentGroup) {
      handleGroupSelect(currentGroup);
      if (currentGroup.group_type === "aggregate") {
        await loadSubGroups();
      }
      return;
    }
  }

  if (selectFirst && groups.value.length > 0) {
    handleGroupSelect(groups.value[0]);
  }
}

// 处理子分组选择，跳转到对应的分组
function handleSubGroupSelect(groupId: number) {
  const targetGroup = groups.value.find(g => g.id === groupId);
  if (targetGroup) {
    handleGroupSelect(targetGroup);
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
          @refresh="() => refreshGroupsAndSelect()"
          @refresh-and-select="id => refreshGroupsAndSelect(id)"
        />
      </div>

      <!-- 右侧主内容区域，占80% -->
      <div class="main-content">
        <!-- 分组信息卡片，更紧凑 -->
        <div class="group-info">
          <group-info-card
            :group="selectedGroup"
            :groups="groups"
            :sub-groups="subGroups"
            @refresh="() => refreshGroupsAndSelect()"
            @delete="() => refreshGroupsAndSelect(undefined, true)"
            @copy-success="group => refreshGroupsAndSelect(group.id)"
          />
        </div>

        <!-- 密钥表格区域 / 子分组列表区域 -->
        <div class="key-table-section">
          <!-- 标准分组显示密钥列表 -->
          <key-table
            v-if="!selectedGroup || selectedGroup.group_type !== 'aggregate'"
            :selected-group="selectedGroup"
          />

          <!-- 聚合分组显示子分组列表 -->
          <sub-group-table
            v-else
            :selected-group="selectedGroup"
            :sub-groups="subGroups"
            :groups="groups"
            :loading="loadingSubGroups"
            @refresh="loadSubGroups"
            @group-select="handleSubGroupSelect"
          />
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
