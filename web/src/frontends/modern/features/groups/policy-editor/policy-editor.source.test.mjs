// 源级文案回归：三语键集合一致、组件模板里写死的 policyEditor 键可解析、weekday 标签齐全且互异。
// UI 控件规范交给 ESLint / check-modern-styles，编译产物的交互交给 runtime 测试，这里不重复。
// 运行：node --test web/src/frontends/modern/features/groups/policy-editor/policy-editor.source.test.mjs
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'

import { parseSfc } from '../../../../../../scripts/policy-test-utils.mjs'
import { enUS, jaJP, zhCN } from '../../../i18n/locales/policy-editor.ts'

const dir = import.meta.dirname
const locales = { 'zh-CN': zhCN, 'en-US': enUS, 'ja-JP': jaJP }

function collectKeys(value, prefix = '', into = new Set()) {
  for (const [key, child] of Object.entries(value)) {
    const next = prefix ? `${prefix}.${key}` : key
    if (child && typeof child === 'object') collectKeys(child, next, into)
    else into.add(next)
  }
  return into
}

function resolve(source, dotted) {
  return dotted.split('.').reduce((current, part) => current?.[part], { policyEditor: source })
}

test('locale contract: key parity, template keys, weekday labels', () => {
  const base = [...collectKeys(zhCN)].sort()
  for (const [name, locale] of Object.entries(locales)) {
    assert.deepEqual([...collectKeys(locale)].sort(), base, `${name} key set`)
    for (const file of readdirSync(dir).filter((entry) => entry.endsWith('.vue'))) {
      const template =
        parseSfc(readFileSync(path.join(dir, file), 'utf8'), { filename: file }).descriptor.template
          ?.content ?? ''
      for (const [, key] of template.matchAll(/\bt\(\s*'([^']+)'/g)) {
        assert.notEqual(resolve(locale, key), undefined, `${file}: ${key} missing in ${name}`)
      }
    }
    const labels = Array.from({ length: 7 }, (_, day) =>
      resolve(locale, `policyEditor.timeWindow.weekday.${day}`),
    )
    assert.deepEqual(
      labels.map((label) => typeof label),
      Array(7).fill('string'),
      name,
    )
    assert.equal(new Set(labels).size, 7, `${name} weekdays must be distinct`)
  }
})
