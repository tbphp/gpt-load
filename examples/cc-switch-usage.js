// 粘贴到 CC Switch 的「用量查询」自定义脚本；使用 GPT-Load AccessKey。
({
  request: {
    url: "{{baseUrl}}".replace(/\/+$/, "").replace(/\/v1$/, "") + "/v1/usage",
    method: "GET",
    headers: {
      Authorization: "Bearer {{apiKey}}",
    },
  },
  extractor: function (response) {
    const result = {
      isValid: response.isValid,
      used: response.used,
      unit: "USD",
    };
    // total=0 代表未配置总额度，仅展示已用量。
    if (response.total > 0) {
      result.total = response.total;
      result.remaining = response.remaining;
    }
    return result;
  },
})
