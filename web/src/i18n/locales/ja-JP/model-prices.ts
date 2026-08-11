export default {
  modelPrices: {
    status: {
      pending: '価格待ち',
      configured: '設定済み',
      unpriced: '価格なしに設定済み',
    },
    fields: {
      input: '入力',
      output: '出力',
      cache_read: 'キャッシュ読み取り',
      cache_write: 'キャッシュ書き込み',
    },
    method: {
      auto_sync: 'カタログ同期',
      reference_price: '参照価格',
      user_set: '手動価格',
    },
    source: {
      channel_catalog_provider: 'Models.dev · {provider} の価格',
      provider_priority_fallback:
        'このチャネルに完全一致するカタログ価格がないため、Models.dev · {provider} を参照',
    },
    matrix: {
      heading: '価格',
      unit: 'USD / 1M',
      thresholdColumn: '段階',
      baseRow: 'デフォルト',
      addTier: '段階を追加',
      removeTier: 'この段階を削除',
      oneHourRule:
        '上流が 1h キャッシュ書き込み Token を返した場合、キャッシュ書き込み × 1.6 で計算します。',
      tierRule: 'リクエスト入力 Token が {threshold} 以上の場合、第 {tier} 段階に一致します。',
      thresholdNotSet: '未入力',
      save: '保存',
      saveFailed: 'モデル価格を保存できません。入力内容は保持されています',
      unpricedConfirm: {
        title: 'このモデルを価格なしにしますか？',
        description: '「{model}」の基本価格と段階価格の枠がすべて未設定になります',
        close: '価格なしの確認を閉じる',
        warning: '今後も Token は記録されますが、コストは 0 と推定され価格なしと表示されます',
        confirm: '価格なしを確認',
      },
      errors: {
        invalid_price: '小数 9 桁以内で、対応範囲内の 0 以上の価格を入力してください',
        threshold_required: 'この段階の入力量しきい値を入力してください',
        threshold_invalid: 'しきい値は 0 以上の整数にしてください',
        threshold_duplicate: 'しきい値は他の段階と重複できません',
        tier_empty: 'この段階には少なくとも 1 つの価格が必要です',
      },
    },
    reset: {
      open: '価格をリセット',
      title: '価格をリセットしますか？',
      description: '「{model}」の手動価格設定をリセットします',
      close: '価格リセットの確認を閉じる',
      warning: '利用可能な場合は自動価格に戻り、ない場合は価格待ちになります',
      confirm: '価格をリセット',
      failed: '価格をリセットできません',
    },
    delete: {
      open: '価格レコードを削除',
      title: '価格レコードを削除しますか？',
      description: '未参照の「{model}」手動価格レコードを削除します',
      close: '価格削除の確認を閉じる',
      warning: '参照されていない手動レコードだけを削除できます',
      confirm: 'レコードを削除',
      failed: '価格レコードを削除できません',
    },
    errors: {
      referenced: 'この価格は {groups} Group から {entries} 件参照されています',
      automaticDeleteForbidden: '自動価格は削除できません。先に手動価格へ変更してください',
    },
  },
} as const
