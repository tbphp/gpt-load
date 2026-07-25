import baseStyles from '@/styles/base.css?raw'

import appDialog from './AppDialog.vue?raw'
import appSelect from './AppSelect.vue?raw'
import queryFeedback from './QueryFeedback.vue?raw'
import statusBadge from './StatusBadge.vue?raw'

describe('shared accessibility styles', () => {
  it('keeps mobile text inputs at 16px and recovery controls at 44px without spinner motion', () => {
    expect(baseStyles).toMatch(/@media \(max-width: 640px\) \{[\s\S]*body \{\s*font-size: 1rem;/)
    expect(baseStyles).toMatch(/input:not\(\[type=['"](?:checkbox|radio)['"]\]\).*font-size: 1rem/s)
    expect(queryFeedback).toMatch(/min-height: 44px/)
    expect(queryFeedback).toMatch(/prefers-reduced-motion: reduce[\s\S]*animation: none/)
    expect(appSelect).toMatch(/\.app-select__item[\s\S]*min-height: 44px/)
  })

  it('keeps keyboard focus visible and long dialogs within the viewport', () => {
    expect(baseStyles).toMatch(
      /:focus-visible\s*\{[\s\S]*outline: 2px solid var\(--color-primary\);[\s\S]*outline-offset: 2px;/,
    )
    expect(appDialog).toMatch(/max-height: calc\(100dvh - 32px\)/)
    expect(appDialog).toMatch(/\.app-dialog__body\s*\{[\s\S]*overflow-y: auto/)
  })

  it('keeps small status text readable while preserving semantic icon color', () => {
    expect(statusBadge).toMatch(
      /\.status-badge--success,\s*\.status-badge--warning,\s*\.status-badge--danger\s*\{[\s\S]*color: var\(--color-text\)/,
    )
    expect(statusBadge).toMatch(/\.status-badge--success svg[\s\S]*color: var\(--color-success\)/)
  })
})
