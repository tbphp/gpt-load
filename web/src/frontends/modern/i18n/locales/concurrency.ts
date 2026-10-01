export const zhCN = {
  label: '并发上限',
  value: '并发 {current} / {limit}',
  limitTag: '并发 {limit}',
  accessKeyLimitHelp: '该访问密钥同时最多处理 {limit} 个请求，跨分组合计；满额立即拒绝。',
  inherit: '继承默认值',
  unlimited: '不限',
  overrideHelp: '留空继承默认值；0 表示不限。满额立即拒绝。',
  limitHelp: '0 表示不限。仅限制数据面并发，满额立即拒绝。',
  global_concurrency_limit: '全局并发上限',
  default_access_key_concurrency_limit: '访问密钥默认并发上限',
  default_group_concurrency_limit: '分组默认并发上限',
}

export const enUS = {
  label: 'Concurrency limit',
  value: 'Concurrency {current} / {limit}',
  limitTag: 'Concurrency {limit}',
  accessKeyLimitHelp:
    'At most {limit} active requests for this access key across groups; reject immediately when full.',
  inherit: 'Inherit default',
  unlimited: 'Unlimited',
  overrideHelp: 'Leave blank to inherit; 0 means unlimited. Reject immediately when full.',
  limitHelp: '0 means unlimited. Applies to data-plane concurrency; reject immediately when full.',
  global_concurrency_limit: 'Global concurrency limit',
  default_access_key_concurrency_limit: 'Default access key concurrency limit',
  default_group_concurrency_limit: 'Default group concurrency limit',
}

export const jaJP = {
  label: '同時実行数の上限',
  value: '同時実行 {current} / {limit}',
  limitTag: '同時実行 {limit}',
  accessKeyLimitHelp:
    'このアクセスキーはグループをまたいで最大 {limit} 件を同時実行できます。上限到達時は即座に拒否します。',
  inherit: 'デフォルトを継承',
  unlimited: '無制限',
  overrideHelp: '空欄でデフォルトを継承、0 で無制限。上限到達時は即座に拒否します。',
  limitHelp: '0 は無制限。データプレーンの同時実行を制限し、上限到達時は即座に拒否します。',
  global_concurrency_limit: '全体の同時実行数上限',
  default_access_key_concurrency_limit: 'アクセスキーのデフォルト同時実行数上限',
  default_group_concurrency_limit: 'グループのデフォルト同時実行数上限',
}
