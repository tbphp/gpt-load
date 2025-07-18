/**
 * Copy text
 */
export async function copy(text: string): Promise<boolean> {
  // navigator.clipboard is only available in secure contexts (HTTPS) or localhost
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (e) {
      console.error("Failed to copy using navigator.clipboard:", e);
    }
  }

  // Fallback: use deprecated but more compatible execCommand
  try {
    const input = document.createElement("input");
    input.style.position = "fixed";
    input.style.opacity = "0";
    input.value = text;
    document.body.appendChild(input);
    input.select();
    const result = document.execCommand("copy");
    document.body.removeChild(input);

    if (!result) {
      console.error("Failed to copy using execCommand");
      return false;
    }
    return true;
  } catch (e) {
    console.error("Error in fallback copy method:", e);
    return false;
  }
}
