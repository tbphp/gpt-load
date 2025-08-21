import http from "@/utils/http";

export interface Setting {
  key: string;
  name: string;
  value: string | number | boolean;
  type: "int" | "string" | "bool";
  min_value?: number;
  description: string;
  required: boolean;
}

export interface SettingCategory {
  category_name: string;
  settings: Setting[];
}

export type SettingsUpdatePayload = Record<string, string | number | boolean>;

export const settingsApi = {
  async getSettings(): Promise<SettingCategory[]> {
    const response = await http.get("/settings");
    return response.data || [];
  },
  updateSettings(data: SettingsUpdatePayload): Promise<void> {
    return http.put("/settings", data);
  },
  async getChannelTypes(): Promise<string[]> {
    const response = await http.get("/channel-types");
    return response.data || [];
  },
};
