export default {
  sections: {
    workspace: 'ワークスペース',
    observe: '運用状況',
    system: 'システム',
  },
  shell: {
    goHome: 'GPT-Load の概要',
    collapseSidebar: 'サイドバーを折りたたむ',
    expandSidebar: 'サイドバーを展開',
    close: '閉じる',
    documentation: 'ドキュメント',
    selfHosted: 'セルフホスト AI ゲートウェイ',
    mobileNavigationDescription: '管理ワークスペースや運用状況のページへ移動します。',
    preview: 'フレームワークのプレビュー',
    previewDescription:
      '業務データはまだ接続されていません。すべての管理機能はクラシック版でご利用いただけます。',
  },
  pages: {
    home: {
      title: '概要',
      description: '設定から運用状況まで、AI ゲートウェイを一元管理します。',
    },
    groups: {
      title: 'グループ',
      description: '上流接続、認証情報、モデル、グループ設定を管理します。',
    },
    models: {
      title: 'モデル',
      description: 'モデルの利用可否、ルーティング機能、価格設定を確認します。',
    },
    accessKeys: {
      title: 'アクセスキー',
      description: 'クライアントのアクセス範囲や利用上限を管理します。',
    },
    usage: {
      title: '使用量',
      description: 'リクエスト数、トークン使用量、推定費用を確認します。',
    },
    logs: {
      title: 'リクエストログ',
      description: 'リクエスト結果、上流への試行、使用量の詳細を追跡します。',
    },
    health: {
      title: '稼働状況',
      description: '認証情報の利用可否、クールダウン、運用上の問題を確認します。',
    },
    inspector: {
      title: 'ルート検査',
      description: 'モデルとグループ、上流認証情報の対応を確認します。',
    },
    settings: {
      title: 'グローバル設定',
      description: 'このブラウザの外観、言語、画面設定を変更します。',
    },
  },
  home: {
    manageGroups: 'グループを管理',
    workspace: {
      title: 'リソースとアクセス',
      description: '上流の機能とクライアントのアクセスを設定します。',
    },
    observe: {
      title: '運用状況',
      description: '使用傾向から個別リクエストまで、必要な画面へ直接移動できます。',
    },
    workflowTitle: '接続の手順',
    workflowDescription: '上流サービスを接続し、クライアントに共通の入口を提供します。',
    workflowGroups: 'グループを作成',
    workflowCredentials: '認証情報とモデルを設定',
    workflowAccess: 'アクセスキーを作成',
  },
  workspace: {
    pendingTitle: 'このワークスペースは開発中です',
    pendingDescription: 'ページの入口が用意されています。業務データと操作は今後順次追加されます。',
    backHome: '概要に戻る',
  },
  appearance: {
    title: '外観と設定',
    description: 'このブラウザだけに適用され、ゲートウェイの動作設定は変わりません。',
    theme: '表示モード',
    themeDescription: 'ライト、ダーク、またはシステムの設定を選択します。',
    language: '表示言語',
    languageDescription: '変更はすぐに反映されます。',
    themes: {
      system: 'システム',
      light: 'ライト',
      dark: 'ダーク',
    },
    persistenceFailed: 'ブラウザに保存できません。設定は今回のアクセス中のみ有効です。',
  },
  quickNavigation: {
    title: 'クイック移動',
    placeholder: 'ページや機能を検索…',
    description: 'ページ名を検索し、矢印キーで選択して Enter キーで移動します。',
    pages: 'ページと機能',
    empty: '一致するページはありません。',
    keyboardHint: '↑ ↓ 選択　Enter 移動　Esc 閉じる',
  },
  navigation: 'メインナビゲーション',
  skipToContent: 'メインコンテンツへ移動',
  settings: 'グローバル設定',
  interfaceSettings: '画面設定',
  homeTitle: '新しい画面',
  homeDescription: '新しい画面は開発中です。すべての管理機能はクラシック版でご利用いただけます。',
  unavailableTitle: 'このページは新しい画面ではまだ利用できません',
  frontend: {
    description:
      'このブラウザで使用する画面を選択します。切り替えると再読み込みされます。他の利用者には影響しません。',
    current: '現在の画面',
    previewNote: 'サムネイルは概略図です。新版のプレビューはデザイン確定後に更新されます。',
    saveFailed: '設定を保存できません。このサイトのブラウザストレージを許可してください。',
    modern: { title: '新版', description: '開発中の新しい管理画面。' },
    classic: { title: 'クラシック版', description: '従来のレイアウトとすべての管理機能。' },
  },
}
