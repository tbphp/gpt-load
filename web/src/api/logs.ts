import i18n from "@/locales";
import type { ApiResponse, Group, LogFilter, LogsResponse } from "@/types/models";
import http from "@/utils/http";

export const logApi = {
  // 获取日志列表
  getLogs: (params: LogFilter): Promise<ApiResponse<LogsResponse>> => {
    return http.get("/logs", { params });
  },

  // 获取分组列表（用于筛选）
  getGroups: (): Promise<ApiResponse<Group[]>> => {
    return http.get("/groups");
  },

  // 导出日志
  exportLogs: (params: Omit<LogFilter, "page" | "page_size">) => {
    const authKey = localStorage.getItem("authKey");
    if (!authKey) {
      window.$message.error(i18n.global.t("auth.noAuthKeyFound"));
      return;
    }

    const queryParams = new URLSearchParams(
      Object.entries(params).reduce(
        (acc, [key, value]) => {
          if (value !== undefined && value !== null && value !== "") {
            acc[key] = String(value);
          }
          return acc;
        },
        {} as Record<string, string>
      )
    );
    queryParams.append("key", authKey);

    const url = `${http.defaults.baseURL}/logs/export?${queryParams.toString()}`;

    const link = document.createElement("a");
    link.href = url;
    link.setAttribute("download", `logs-${Date.now()}.csv`);
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  },
};
