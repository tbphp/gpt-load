export default {
  common: {
    appName: 'GPT-Load',
    retry: '重试',
    changeKey: '更换 AUTH_KEY',
  },
  auth: {
    loginTitle: '登录管理界面',
    loginDescription: '输入管理 AUTH_KEY 以访问控制面。',
    keyLabel: 'AUTH_KEY',
    keyPlaceholder: '输入 AUTH_KEY',
    submit: '登录',
    submitting: '正在验证…',
    required: '请输入 AUTH_KEY',
    invalidFormat: 'AUTH_KEY 不能包含空白字符',
    invalid: 'AUTH_KEY 无效',
    locked: '认证尝试过多，请在 {seconds} 秒后重试。',
    network: '无法连接到管理 API，请检查服务后重试。',
    invalidResponse: '管理 API 返回了无法识别的响应。',
    checking: '正在验证当前会话…',
    whereTitle: 'AUTH_KEY 在哪里？',
    whereBody:
      '可使用 AUTH_KEY 环境变量；留空时服务会在 DATA_DIR/auth.key 生成。日志只显示文件路径，不包含完整密钥。',
    dockerHint:
      "Docker Compose 可运行：docker compose exec gpt-load sh -c 'cat /app/data/auth.key'",
  },
} as const
