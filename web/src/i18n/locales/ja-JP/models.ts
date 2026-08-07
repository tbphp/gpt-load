export default {
  models: {
    title: 'モデル',
    loading: 'モデルを読み込み中…',
    loadFailed: 'モデルを読み込めません',
    stale: 'バックグラウンド更新に失敗したため、現在のモデル情報は古い可能性があります',
    context:
      '価格は今後のコスト概算用の USD / 100 万 tokens であり、上流の実際の請求額ではありません',
    result: '{total} 件中 {shown} 件のクライアントモデルを表示',
    actions: {
      sync: 'モデルを同期',
    },
    sync: {
      succeeded: 'モデルカタログと自動価格を同期しました',
      failed: 'モデルカタログと価格を同期できません',
    },
    status: {
      models: 'モデル {count} 件',
      pending: '価格待ち {count} 件',
      unit: 'USD / 100 万 tokens（概算）',
    },
    catalog: {
      available: 'Models.dev カタログ利用可能',
      stale: '同期失敗・前回成功したカタログを使用中',
      unavailable: 'Models.dev カタログ利用不可',
      lastSuccess: '最終同期',
      lastCheck: '最終確認',
    },
    filters: {
      region: 'モデルを絞り込む',
      searchLabel: '検索',
      searchPlaceholder: 'クライアントモデル、上流モデル、プロバイダー、Group',
      clearSearch: '検索をクリア',
      groupStatusLabel: 'Group の状態',
      pricingStatusLabel: '価格状態',
      reset: '条件をリセット',
      groupStatus: {
        enabled: '有効な Group',
        all: 'すべての Group',
      },
      pricingStatus: {
        all: 'すべての価格',
        pending: '価格待ち',
        configured: '設定済み',
      },
    },
    empty: {
      title: '表示できるモデルがありません',
      description: '有効な Group にモデルを設定するか、モデルカタログを同期してください',
      noResultsTitle: '条件に一致するモデルがありません',
      noResultsDescription: '検索、Group の状態、価格状態を変更して再度お試しください',
    },
    index: {
      label: 'クライアントモデル一覧',
      priceCount: '価格 {count} 件',
      upstreamCount: '上流 {count} 件',
      copy: 'クライアントモデル {model} をコピー',
      copySucceeded: 'クライアントモデルをコピーしました',
      copyFailed: 'クライアントモデルをコピーできません',
    },
    inspector: {
      direct: '同名で直接転送',
      alias: '{model} にマッピング',
      noCatalog: 'Models.dev カタログ情報なし',
      scopeOption: '{label} {kind}',
    },
    detail: {
      upstreamModel: '上流モデル',
      routeGroups: 'ルート Group',
      groupEnabled: '有効',
      groupDisabled: '無効',
      globalImpact: 'この価格は合計 {count} Group に影響します',
      catalogReference: {
        actual_provider: 'Models.dev · {provider}',
        reference_provider: 'Models.dev 参照プロバイダー · {provider}',
      },
      specs: {
        context: 'コンテキスト',
        maxOutput: '最大出力',
        modalities: 'モダリティ',
      },
      capabilities: {
        attachment: '添付',
        reasoning: '推論',
        tool_call: 'ツール呼び出し',
        structured_output: '構造化出力',
        temperature: 'Temperature',
      },
      openWeights: 'オープンウェイト',
    },
  },
} as const
