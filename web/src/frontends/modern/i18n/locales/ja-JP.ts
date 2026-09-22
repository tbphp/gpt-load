import { jaJP as requestRedaction } from './request-redaction'
import { jaJP as experimental } from './experimental'
import { protocolMessages as protocols } from '../protocols'
import { jaJP as inspector } from './inspector'
import { jaJP as home } from './home'
import { jaJP as accessKeys } from './access-keys'
import { jaJP as logs } from './logs'
import { jaJP as usage } from './usage'
import { jaJP as health } from './health'
import { jaJP as credentialCards } from './credential-cards'
import { jaJP as groupWorkflows } from './group-workflows'
import { jaJP as parameterRules } from './parameter-rules'
import { jaJP as groupDetail } from './group-detail'
import { jaJP as groupMessages } from './groups'
import { jaJP as ui } from './ui'
import { jaJP as groupCreate } from './group-create'
import { jaJP as modelSelection } from './model-selection'
import { jaJP as modelManager } from './model-manager'
import { jaJP as settingsForm } from './settings-form'
import { jaJP as autoModel } from './auto-model'
import { jaJP as subscriptions } from './subscriptions'

export default {
  requestRedaction,
  ...experimental,
  home,
  inspector,
  protocols,
  logs,
  usage,
  health,
  accessKeys,
  groupWorkflows,
  parameterRules,
  credentialCards,
  groupDetail,
  subscriptions,
  modelSelection,
  modelManager,
  settingsForm,
  autoModel,
  groupCreate,
  ui,
  ...groupMessages,
  sections: {
    workspace: 'ワークスペース',
    observe: '運用状況',
    system: 'システム',
  },
  shell: {
    refresh: '更新',
    refreshedAt: '{time} 更新',
    importCredentials: 'キーをインポート',
    goHome: 'GPT-Load の概要',
    mascotHint:
      'マスコットを長押し、またはフォーカス中にスペースで遊べます。Enter で概要へ戻ります。',
    collapseSidebar: 'サイドバーを折りたたむ',
    expandSidebar: 'サイドバーを展開',
    close: '閉じる',
    documentation: '利用ガイド',
    sponsor: 'スポンサー',
    mobileNavigationDescription: '管理ワークスペースや運用状況のページへ移動します。',
    navigationFailed: 'ページを読み込めません。再読み込みしてください。',
    reload: '再読み込み',
  },
  auth: {
    title: 'GPT-Load にログイン',
    description: '管理者キーまたはアクセスキーを入力すると、サーバーが権限を判定します。',
    keyLabel: 'ログインキー',
    keyPlaceholder: 'AUTH_KEY またはアクセスキーを入力',
    reveal: 'ログインキーを表示',
    conceal: 'ログインキーを隠す',
    submit: 'ログイン',
    submitting: '確認中…',
    required: 'ログインキーを入力してください',
    invalidFormat: 'ログインキーに空白文字は使用できません',
    invalid: 'ログインキーが無効です。確認して再試行してください。',
    locked: '認証試行回数が多すぎます。{seconds} 秒後に再試行してください。',
    lockEnded: 'セッションを再確認できます。',
    network: '管理 API に接続できません。サービスを確認して再試行してください。',
    invalidResponse: '管理 API から認識できない応答が返されました。後でもう一度お試しください。',
    restoreTitle: 'ログイン状態を確認',
    checking: '現在のセッションを確認中…',
    retry: '再確認',
    changeKey: '別のキーを使用',
    signedIn: 'ログインしました。ページを開いています…',
    continue: '続ける',
    remember: 'ログイン状態を保持',
    logout: 'ログアウト',
    readOnly: 'アクセスキー · 閲覧専用',
    readOnlyDescription:
      'このキーの概要、モデル、使用量、リクエスト履歴のみ閲覧でき、システム設定は変更できません。',
    help: {
      title: 'どのキーを使えますか？',
      accessKeyTitle: 'アクセスキー',
      accessKey: '配布されたアクセスキーで、そのキーのデータを閲覧できます。設定は変更できません。',
      adminTitle: '管理者キー',
      admin: 'AUTH_KEY が設定されている場合は、デプロイ環境にある値を使用します。',
      file: 'AUTH_KEY が未設定の場合、キーは {path} にあります。コンテナの既定パスは {containerPath} です。',
      docker:
        'Docker の場合は自分の端末からコンテナに入り、ファイルを確認してください。キーをログやチャットに貼り付けないでください。',
    },
  },
  pages: {
    home: { title: '概要' },
    groups: { title: 'グループ' },
    groupDetail: { title: 'グループの詳細' },
    models: { title: 'モデル' },
    accessKeys: { title: 'アクセスキー' },
    usage: { title: '使用量' },
    logs: { title: 'リクエストログ' },
    health: { title: '稼働状況' },
    inspector: { title: 'ルート検査' },
    settings: { title: 'グローバル設定' },
    import: { title: '認証情報のインポート' },
    login: { title: 'ログイン' },
  },
  notFound: {
    title: 'ページが見つかりません',
    description: 'ページのアドレスを確認するか、概要に戻ってください。',
    backHome: '概要に戻る',
  },
  appearance: {
    theme: '表示モード',
    language: '表示言語',
    themes: {
      system: 'システム',
      light: 'ライト',
      dark: 'ダーク',
    },
    persistenceFailed: 'ブラウザに保存できません。設定は今回のアクセス中のみ有効です。',
  },
  system: {
    currentVersion: '現在のバージョン：{version}',
    loadingVersion: '読み込み中…',
    versionUnavailable: 'バージョン不明',
    checkUpdate: '更新を確認',
    checking: '確認中…',
    latestVersion: '最新バージョンです',
    updateAvailable: '新バージョン {version}',
    checkFailed: '確認に失敗しました。再試行してください。',
    authRequired: '管理者ログインが必要です',
  },
  navigation: 'メインナビゲーション',
  skipToContent: 'メインコンテンツへ移動',
  interfaceSettings: '画面設定',
  frontend: {
    description: '切り替えると再読み込みされ、このブラウザにのみ適用されます。',
    current: '現在の画面',
    saveFailed: '設定を保存できません。このサイトのブラウザストレージを許可してください。',
    modern: { title: 'モダン版' },
    classic: { title: 'クラシック版' },
  },
}
