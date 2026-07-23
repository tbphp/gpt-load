export default {
  common: {
    appName: 'GPT-Load',
    retry: '再試行',
    changeKey: 'AUTH_KEY を変更',
  },
  auth: {
    loginTitle: '管理画面にログイン',
    loginDescription:
      'コントロールプレーンにアクセスするための管理用 AUTH_KEY を入力してください。',
    keyLabel: 'AUTH_KEY',
    keyPlaceholder: 'AUTH_KEY を入力',
    submit: 'ログイン',
    submitting: '確認中…',
    required: 'AUTH_KEY を入力してください',
    invalidFormat: 'AUTH_KEY に空白文字は使用できません',
    invalid: 'AUTH_KEY が無効です',
    locked: '認証試行回数が多すぎます。{seconds} 秒後に再試行してください。',
    network: '管理 API に接続できません。サービスを確認して再試行してください。',
    invalidResponse: '管理 API から認識できない応答が返されました。',
    checking: '現在のセッションを確認しています…',
    whereTitle: 'AUTH_KEY はどこにありますか？',
    whereBody:
      'AUTH_KEY 環境変数を設定できます。空の場合、サービスは DATA_DIR/auth.key を生成します。ログに表示されるのはファイルパスだけで、完全なキーは表示されません。',
    dockerHint:
      "Docker Compose では次を実行します：docker compose exec gpt-load sh -c 'cat /app/data/auth.key'",
  },
} as const
