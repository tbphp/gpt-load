import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { compileStyle, parse } from 'vue/compiler-sfc'

import { breakpoints } from '../src/frontends/modern/app/breakpoints.ts'

const root = fileURLToPath(new URL('../src/frontends/modern/', import.meta.url))
const tokenFile = path.join(root, 'styles/tokens.css')
const declarations = []
const mediaQueries = []
const issues = []
const definedTokens = new Set()

// 页面只组合公共控件；隐藏表单值不属于视觉控件。
function checkTemplateRules(node, filename) {
  if (!node) return
  if (node.type === 1) {
    const hiddenInput =
      node.tag === 'input' &&
      node.props.some(
        (prop) => prop.type === 6 && prop.name === 'type' && prop.value?.content === 'hidden',
      )
    if (
      filename.startsWith(path.join(root, 'features') + path.sep) &&
      ['button', 'input', 'select', 'textarea', 'svg'].includes(node.tag) &&
      !hiddenInput
    ) {
      issues.push(
        `${path.relative(root, filename)}:${node.loc.start.line}: 业务页面的 <${node.tag}> 必须使用 components/ui 公共组件`,
      )
    }
    const hasTitle = node.props.some(
      (prop) =>
        (prop.type === 6 && prop.name === 'title') ||
        (prop.type === 7 &&
          prop.name === 'bind' &&
          prop.arg?.isStatic &&
          prop.arg.content === 'title'),
    )
    if (node.tagType === 0 && (hasTitle || node.tag === 'title')) {
      issues.push(
        `${path.relative(root, filename)}:${node.loc.start.line}: 鼠标提示必须使用 AppTooltip，不使用原生 title`,
      )
    }
  }
  for (const child of node.children ?? []) checkTemplateRules(child, filename)
}

async function visit(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const filename = path.join(directory, entry.name)
    if (entry.isDirectory()) {
      await visit(filename)
      continue
    }
    if (!/\.(?:css|vue)$/u.test(filename)) continue
    const source = await readFile(filename, 'utf8')
    const descriptor = filename.endsWith('.vue')
      ? parse(source, { filename }).descriptor
      : undefined
    checkTemplateRules(descriptor?.template?.ast, filename)
    const blocks = filename.endsWith('.css')
      ? [{ content: source, offset: 0 }]
      : descriptor.styles.map((style) => ({
          content: style.content,
          offset: style.loc.start.line - 1,
        }))
    for (const block of blocks) {
      const result = compileStyle({
        filename,
        id: 'modern-style-check',
        source: block.content,
        postcssPlugins: [
          {
            postcssPlugin: 'modern-style-check',
            Declaration(node) {
              const location = `${path.relative(root, filename)}:${node.source.start.line + block.offset}`
              declarations.push({ filename, location, property: node.prop, value: node.value })
              if (node.prop.startsWith('--modern-')) definedTokens.add(node.prop)
            },
            AtRule(node) {
              if (node.name === 'media') mediaQueries.push({ filename, value: node.params })
            },
          },
        ],
      })
      for (const error of result.errors) issues.push(`${filename}: ${String(error)}`)
    }
  }
}

await visit(root)

const visualProperty =
  /^(?:color|background(?:-color)?|(?:border|outline)(?:-(?:top|right|bottom|left))?-color|border(?:-.*)?-radius|border-radius|fill|stroke|stroke-width|font(?:-family|-size|-weight)?|line-height|letter-spacing|(?:box|text)-shadow|scrollbar-color|z-index|opacity)$/u
const spacingProperty =
  /^(?:margin|padding|gap|row-gap|column-gap|border|outline|inset|scroll-margin|scroll-padding)(?:-|$)/u
const rawColor = /#[\da-f]{3,8}\b|\b(?:rgba?|hsla?|hwb|oklch|lch|oklab|lab|color|light-dark)\(/iu
const rawLength = /(?:\d*\.)?\d+(?:px|rem|em|ch|ex|lh)\b/u
const rawDuration = /(?:\d*\.)?\d+m?s\b/u
const neutralValues = new Set([
  '0',
  'none',
  'normal',
  'inherit',
  'initial',
  'unset',
  'revert',
  'transparent',
  'currentColor',
])

for (const { filename, location, property, value } of declarations) {
  for (const match of value.matchAll(/var\(\s*(--modern-[\w-]+)/gu)) {
    if (!definedTokens.has(match[1])) issues.push(`${location}: 未定义的视觉变量 ${match[1]}`)
  }
  if (filename === tokenFile) continue
  const hasToken = value.includes('var(--modern-')
  if (rawColor.test(value)) {
    issues.push(`${location}: ${property} 的颜色必须取自 styles/tokens.css`)
  } else if (
    visualProperty.test(property) &&
    !hasToken &&
    !neutralValues.has(value) &&
    !(property === 'opacity' && value === '1')
  ) {
    issues.push(`${location}: ${property} 必须使用视觉变量`)
  }
  if (spacingProperty.test(property) && rawLength.test(value)) {
    issues.push(`${location}: ${property} 的间距或边框尺寸必须使用视觉变量`)
  }
  if (/^(?:transition|animation)(?:-|$)/u.test(property) && rawDuration.test(value)) {
    issues.push(`${location}: ${property} 的时长必须使用视觉变量`)
  }
}

// CSS 媒体条件不支持 var()，允许的像素阈值从同一份 JS 定义读取。
const widths = new Set(Object.values(breakpoints))
for (const { filename, value } of mediaQueries) {
  for (const match of value.matchAll(/(?:min-width|max-width|width)\s*(?::|[<>]=?)\s*(\d+)px/gu)) {
    if (!widths.has(Number(match[1]))) {
      issues.push(`${path.relative(root, filename)}: 未约定的断点 ${match[1]}px`)
    }
  }
}

if (issues.length) {
  console.error(issues.join('\n'))
  process.exitCode = 1
} else {
  console.log('新版视觉变量、公共控件复用与响应断点检查通过')
}
