import type {
  APIKey,
  Group,
  GroupConfigOption,
  GroupStatsResponse,
  KeyStatus,
  TaskInfo,
} from "@/types/models";
import http from "@/utils/http";

export const keysApi = {
  // 获取所有分组
  async getGroups(): Promise<Group[]> {
    const res = await http.get("/groups");
    return res.data || [];
  },

  // 创建分组
  async createGroup(group: Partial<Group>): Promise<Group> {
    const res = await http.post("/groups", group);
    return res.data;
  },

  // 更新分组
  async updateGroup(groupId: number, group: Partial<Group>): Promise<Group> {
    const res = await http.put(`/groups/${groupId}`, group);
    return res.data;
  },

  // 删除分组
  deleteGroup(groupId: number): Promise<void> {
    return http.delete(`/groups/${groupId}`);
  },

  // 获取分组统计信息
  async getGroupStats(groupId: number): Promise<GroupStatsResponse> {
    const res = await http.get(`/groups/${groupId}/stats`);
    return res.data;
  },

  // 获取分组可配置参数
  async getGroupConfigOptions(): Promise<GroupConfigOption[]> {
    const res = await http.get("/groups/config-options");
    return res.data || [];
  },

  // 复制分组
  async copyGroup(
    groupId: number,
    copyData: {
      copy_keys: "none" | "valid_only" | "all";
    }
  ): Promise<{
    group: Group;
  }> {
    const res = await http.post(`/groups/${groupId}/copy`, copyData);
    return res.data;
  },

  // 获取分组列表（简化版）
  async listGroups(): Promise<Group[]> {
    const res = await http.get("/groups/list");
    return res.data || [];
  },

  // 获取分组的密钥列表
  async getGroupKeys(params: {
    group_id: number;
    page: number;
    page_size: number;
    key_value?: string;
    status?: KeyStatus;
  }): Promise<{
    items: APIKey[];
    pagination: {
      total_items: number;
      total_pages: number;
    };
  }> {
    const res = await http.get("/keys", { params });
    return res.data;
  },

  // 批量添加密钥-已弃用
  async addMultipleKeys(
    group_id: number,
    keys_text: string
  ): Promise<{
    added_count: number;
    ignored_count: number;
    total_in_group: number;
  }> {
    const res = await http.post("/keys/add-multiple", {
      group_id,
      keys_text,
    });
    return res.data;
  },

  // 异步批量添加密钥
  async addKeysAsync(group_id: number, keys_text: string): Promise<TaskInfo> {
    const res = await http.post("/keys/add-async", {
      group_id,
      keys_text,
    });
    return res.data;
  },

  // 测试密钥
  async testKeys(
    group_id: number,
    keys_text: string
  ): Promise<{
    results: {
      key_value: string;
      is_valid: boolean;
      error: string;
    }[];
    total_duration: number;
  }> {
    const res = await http.post(
      "/keys/test-multiple",
      {
        group_id,
        keys_text,
      },
      {
        hideMessage: true,
      }
    );
    return res.data;
  },

  // 删除密钥
  async deleteKeys(
    group_id: number,
    keys_text: string
  ): Promise<{ deleted_count: number; ignored_count: number; total_in_group: number }> {
    const res = await http.post("/keys/delete-multiple", {
      group_id,
      keys_text,
    });
    return res.data;
  },

  // 异步批量删除密钥
  async deleteKeysAsync(group_id: number, keys_text: string): Promise<TaskInfo> {
    const res = await http.post("/keys/delete-async", {
      group_id,
      keys_text,
    });
    return res.data;
  },

  // 测试密钥
  restoreKeys(group_id: number, keys_text: string): Promise<null> {
    return http.post("/keys/restore-multiple", {
      group_id,
      keys_text,
    });
  },

  // 恢复所有无效密钥
  restoreAllInvalidKeys(group_id: number): Promise<void> {
    return http.post("/keys/restore-all-invalid", { group_id });
  },

  // 清空所有无效密钥
  clearAllInvalidKeys(group_id: number): Promise<{ data: { message: string } }> {
    return http.post(
      "/keys/clear-all-invalid",
      { group_id },
      {
        hideMessage: true,
      }
    );
  },

  // 清空所有密钥
  clearAllKeys(group_id: number): Promise<{ data: { message: string } }> {
    return http.post(
      "/keys/clear-all",
      { group_id },
      {
        hideMessage: true,
      }
    );
  },

  // 导出密钥
  exportKeys(groupId: number, status: "all" | "active" | "invalid" = "all") {
    const authKey = localStorage.getItem("authKey");
    if (!authKey) {
      window.$message.error("未找到认证信息，无法导出");
      return;
    }

    const params = new URLSearchParams({
      group_id: groupId.toString(),
      key: authKey,
    });

    if (status !== "all") {
      params.append("status", status);
    }

    const url = `${http.defaults.baseURL}/keys/export?${params.toString()}`;

    const link = document.createElement("a");
    link.href = url;
    link.setAttribute("download", `keys-group_${groupId}-${status}-${Date.now()}.txt`);
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  },

  // 验证分组密钥
  async validateGroupKeys(
    groupId: number,
    status?: "active" | "invalid"
  ): Promise<{
    is_running: boolean;
    group_name: string;
    processed: number;
    total: number;
    started_at: string;
  }> {
    const payload: { group_id: number; status?: string } = { group_id: groupId };
    if (status) {
      payload.status = status;
    }
    const res = await http.post("/keys/validate-group", payload);
    return res.data;
  },

  // 获取任务状态
  async getTaskStatus(): Promise<TaskInfo> {
    const res = await http.get("/tasks/status");
    return res.data;
  },
};
