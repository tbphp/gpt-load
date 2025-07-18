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
  // Get all groups
  async getGroups(): Promise<Group[]> {
    const res = await http.get("/groups");
    return res.data || [];
  },

  // Create group
  async createGroup(group: Partial<Group>): Promise<Group> {
    const res = await http.post("/groups", group);
    return res.data;
  },

  // Update group
  async updateGroup(groupId: number, group: Partial<Group>): Promise<Group> {
    const res = await http.put(`/groups/${groupId}`, group);
    return res.data;
  },

  // Delete group
  deleteGroup(groupId: number): Promise<void> {
    return http.delete(`/groups/${groupId}`);
  },

  // Get group statistics
  async getGroupStats(groupId: number): Promise<GroupStatsResponse> {
    const res = await http.get(`/groups/${groupId}/stats`);
    return res.data;
  },

  // Get configurable parameters for group
  async getGroupConfigOptions(): Promise<GroupConfigOption[]> {
    const res = await http.get("/groups/config-options");
    return res.data || [];
  },

  // Get key list for a group
  async getGroupKeys(params: {
    group_id: number;
    page: number;
    page_size: number;
    key?: string;
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

  // Add multiple keys
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

  // Test keys
  async testKeys(
    group_id: number,
    keys_text: string
  ): Promise<
    {
      key_value: string;
      is_valid: boolean;
      error: string;
    }[]
  > {
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

  // Delete keys
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

  // Test keys
  restoreKeys(group_id: number, keys_text: string): Promise<null> {
    return http.post("/keys/restore-multiple", {
      group_id,
      keys_text,
    });
  },

  // Restore all invalid keys
  restoreAllInvalidKeys(group_id: number): Promise<void> {
    return http.post("/keys/restore-all-invalid", { group_id });
  },

  // Clear all invalid keys
  clearAllInvalidKeys(group_id: number): Promise<{ data: { message: string } }> {
    return http.post(
      "/keys/clear-all-invalid",
      { group_id },
      {
        hideMessage: true,
      }
    );
  },

  // Export keys
  exportKeys(groupId: number, status: "all" | "active" | "invalid" = "all") {
    let url = `${http.defaults.baseURL}/groups/${groupId}/keys/export`;
    if (status !== "all") {
      url += `?status=${status}`;
    }

    // Create hidden <a> tag to implement download
    const link = document.createElement("a");
    link.href = url;
    link.download = `group-${groupId}-keys-${status}.txt`;
    link.style.display = "none";

    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  },

  // Validate group keys
  async validateGroupKeys(groupId: number): Promise<{
    is_running: boolean;
    group_name: string;
    processed: number;
    total: number;
    started_at: string;
  }> {
    const res = await http.post("/keys/validate-group", { group_id: groupId });
    return res.data;
  },

  // Get task status
  async getTaskStatus(): Promise<TaskInfo> {
    const res = await http.get("/tasks/status");
    return res.data;
  },
};
