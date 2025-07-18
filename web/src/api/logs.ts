import type { ApiResponse, Group, LogFilter, LogsResponse } from "@/types/models";
import http from "@/utils/http";

export const logApi = {
  // Get log list
  getLogs: (params: LogFilter): Promise<ApiResponse<LogsResponse>> => {
    return http.get("/logs", { params });
  },

  // Get group list (for filtering)
  getGroups: (): Promise<ApiResponse<Group[]>> => {
    return http.get("/groups");
  },
};
