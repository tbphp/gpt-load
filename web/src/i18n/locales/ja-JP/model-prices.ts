export default {
  modelPrices: {
    title: 'モデル価格',
    description:
      'Token コストの推定に使うモデル価格を管理します。組み込みルールは読み取り専用です。',
    back: '設定に戻る',
    add: '上書きを追加',
    loading: 'モデル価格を読み込み中…',
    loadFailed: 'モデル価格を読み込めません。',
    stale: 'バックグラウンド更新に失敗したため、モデル価格が古い可能性があります。',
    priceUnit: '100 万 Token あたりの米ドル',
    historyNote: '価格変更は今後のコスト推定だけに影響し、過去の使用量やコストは再計算されません。',
    modelIdentityNote:
      'ルールはルーティング後のアップストリームモデル ID に一致し、同じモデル ID はすべての Group で 1 つのグローバル価格を共有します。',
    precedenceNote:
      'ユーザー上書きは、すべての組み込み完全一致ルールとプレフィックスルールより優先されます。',
    wholeRuleNote:
      'ユーザー上書きは 5 枠のルール全体を置き換え、未設定の枠は組み込み値へフォールバックしません。',
    scrollHint: '横方向にスクロールすると、モデル価格のすべての列を確認できます。',
    details: '価格フィールドとメタデータ',
    sourceUnavailable: 'ソースを利用できません',
    notConfigured: '未設定',
    explicitlyFree: '$0 · 明示的に無料',
    configuredPrice: '${price} / 1M',
    globalUserOverride: 'グローバルユーザー上書き',
    kind: {
      exact: '完全一致モデル',
      prefix: 'プレフィックスルール',
      global: 'グローバルルール',
    },
    source: {
      builtin: '組み込み',
      user: '明示的な上書き',
    },
    fields: {
      uncached_input: 'キャッシュなし入力',
      cache_read: 'キャッシュ読み取り',
      cache_write_5m: '5 分キャッシュ書き込み',
      cache_write_1h: '1 時間キャッシュ書き込み',
      output: '出力',
    },
    table: {
      pattern: 'モデルパターン',
      kind: 'ルール種別',
      source: '出典',
      updatedAt: '更新日時',
      actions: '操作',
    },
    builtin: {
      title: '組み込み価格',
      description: 'このバージョンに同梱された読み取り専用の参考価格です。',
      empty: '組み込みモデル価格がありません',
      emptyDescription: 'このビルドから組み込み価格ルールが返されませんでした。',
      caption: '組み込みモデル価格ルール',
      source: '公式ソース',
      createOverride: '上書きを作成',
      longContext: {
        label: '長いコンテキストの価格ポリシー',
        summary:
          '入力 Token が {threshold} を超える場合、すべての入力価格は ×{inputMultiplier}、出力価格は ×{outputMultiplier} になります。',
      },
    },
    overrides: {
      title: 'ユーザー上書き',
      description: '保存した各パターンは、5 つの明示的な価格枠で一致ルール全体を置き換えます。',
      empty: '上書きが未設定です',
      emptyDescription:
        '必要に応じて完全一致、末尾星印プレフィックス、グローバル上書きを追加します。',
      caption: 'ユーザーモデル価格上書き',
      edit: '編集',
    },
    drawer: {
      addTitle: 'モデル価格上書きを追加',
      builtinTitle: '組み込み価格から上書きを作成',
      editTitle: 'モデル価格上書きを編集',
      description: '完全一致、プレフィックス、グローバル価格ルールを設定します。',
      close: 'モデル価格エディターを閉じる',
      pattern: 'モデルパターン',
      patternDescription:
        '完全なモデル ID、末尾の 1 個の * によるプレフィックス、または全モデル用の * を使います。',
      patternReadonly:
        '編集中はパターンを変更できません。別のパターンは新しい上書きとして追加します。',
      prices: '推定価格',
      priceDescription: '100 万 Token あたりの米ドルです。空欄は未設定、0 は明示的なゼロです。',
      wholeReplacement:
        '保存すると、このパターンのルール全体を置き換えます。未設定値を含む 5 つの価格枠をすべて送信します。',
      globalWarning:
        'すべてのユーザールールは組み込みルールより優先されます。* はすべての組み込み価格ルールを覆い隠します。より具体的なユーザールールは引き続き * より優先されます。',
      globalConfirm: '* がすべての組み込み価格ルールを覆い隠すことを理解しました。',
      globalDialog: {
        title: 'グローバルユーザー価格オーバーライドを作成しますか？',
        description:
          '単独の * は、より具体的なユーザー上書きがない全モデルの推定価格ルールを変更します。',
        close: 'グローバル価格上書きの確認を閉じる',
        precedence:
          'このグローバルユーザー上書きは、すべての組み込み完全一致ルールとプレフィックスルールより優先されます。',
        noFallback:
          '未設定の価格枠はフォールバックせず、5 枠のユーザールール全体が組み込みルールを置き換えます。',
        futureOnly:
          '変更は今後の完了済みまたは Emit リクエストだけに適用され、履歴は再計算されません。',
        reset:
          'リセットすると、今後のリクエストでは残りのユーザールールと組み込みルールが復元されます。',
        confirm: 'グローバル上書きを作成',
      },
      save: '上書きを保存',
      saveFailed: 'モデル価格上書きを保存できません。入力内容は保持されています。',
      errors: {
        required: 'モデルパターンを入力してください。',
        too_long: 'UTF-8 で 255 バイト以内にしてください。',
        surrounding_whitespace: '先頭または末尾の空白を削除してください。',
        control_character: '制御文字は使用できません。',
        question_mark: '疑問符は使用できません。',
        star_position: '星印は 1 個までで、末尾にのみ使用できます。',
        invalid_price: '0 以上の有限数を入力してください。',
        all_empty: '5 つの価格枠のうち少なくとも 1 つを設定してください。',
      },
    },
    reset: {
      open: 'リセット',
      title: 'このモデル価格上書きをリセットしますか？',
      description: '「{pattern}」の上書きルールを削除します。',
      close: 'モデル価格リセット確認を閉じる',
      warning:
        '削除後は次に適用可能なユーザールールを先に使い、その後に組み込みルールへフォールバックします。ユーザールールにも組み込みルールにも一致しない場合、モデルは価格未設定になる可能性があります。削除するのはこの上書きだけで、過去の使用量やコストは再計算されません。',
      confirm: '上書きをリセット',
      failed: 'モデル価格上書きをリセットできません。',
    },
  },
} as const
