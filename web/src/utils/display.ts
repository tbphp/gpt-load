import type { Group, SubGroupInfo } from "@/types/models";

/**
 * Formats a string from camelCase, snake_case, or kebab-case
 * into a more readable format with spaces and capitalized words.
 *
 * @param name The input string.
 * @returns The formatted string.
 *
 * @example
 * formatDisplayName("myGroupName")      // "My Group Name"
 * formatDisplayName("my_group_name")    // "My Group Name"
 * formatDisplayName("my-group-name")    // "My Group Name"
 * formatDisplayName("MyGroup")          // "My Group"
 */
export function formatDisplayName(name: string): string {
  if (!name) {
    return "";
  }

  // Replace snake_case and kebab-case with spaces, and add a space before uppercase letters in camelCase.
  const result = name.replace(/[_-]/g, " ").replace(/([a-z])([A-Z])/g, "$1 $2");

  // Capitalize the first letter of each word.
  return result
    .split(" ")
    .filter(word => word.length > 0)
    .map(word => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

/**
 * Gets the display name for a group or subgroup, falling back to a formatted version of its name.
 * @param group The group or subgroup object.
 * @returns The display name for the group.
 */
export function getGroupDisplayName(group: Group | SubGroupInfo): string {
  return group.display_name || formatDisplayName(group.name);
}

/**
 * Masks a long key string for display.
 * @param key The key string.
 * @returns The masked key.
 */
export function maskKey(key: string): string {
  if (!key || key.length <= 8) {
    return key || "";
  }
  return `${key.substring(0, 4)}...${key.substring(key.length - 4)}`;
}

/**
 * Masks a comma-separated string of keys.
 * @param keys The comma-separated keys string.
 * @returns The masked keys string.
 */
export function maskProxyKeys(keys: string): string {
  if (!keys) {
    return "";
  }
  return keys
    .split(",")
    .map(key => maskKey(key.trim()))
    .join(", ");
}
