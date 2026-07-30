import baseStyles from '@/styles/base.css?raw'

import appDialog from './AppDialog.vue?raw'
import appSelect from './AppSelect.vue?raw'
import appShell from '@/app/AppShell.vue?raw'
import connectionPlaceholder from '@/features/home/ConnectionPlaceholder.vue?raw'
import homeLede from '@/features/home/HomeLede.vue?raw'
import homeView from '@/features/home/HomeView.vue?raw'
import dataTable from './DataTable.vue?raw'
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
      /:focus-visible\s*\{[\s\S]*outline: 2px solid var\(--color-focus\);[\s\S]*outline-offset: 2px;/,
    )
    expect(appDialog).toMatch(/max-height: calc\(100dvh - 32px\)/)
    expect(appDialog).toMatch(/\.app-dialog__body\s*\{[\s\S]*overflow-y: auto/)
  })

  it('keeps small status text readable while preserving semantic icon color', () => {
    expect(statusBadge).toMatch(
      /\.status-badge--success,\s*\.status-badge--neutral,\s*\.status-badge--warning,\s*\.status-badge--danger\s*\{[\s\S]*color: var\(--color-text\)/,
    )
    expect(statusBadge).toMatch(/\.status-badge--success svg[\s\S]*color: var\(--color-success\)/)
    expect(statusBadge).toMatch(
      /\.status-badge--neutral\s*\{[\s\S]*background: var\(--color-neutral-bg\);/,
    )
    expect(statusBadge).toMatch(/\.status-badge--neutral svg[\s\S]*color: var\(--color-neutral\)/)
  })

  it('keeps the 375, 768, 1024 and 1440 responsive layout contract explicit', () => {
    expect(dataTable).toMatch(
      /@media \(max-width: 759px\)[\s\S]*\[data-column-priority='low'\][\s\S]*display: none/,
    )
    expect(homeView).toMatch(
      /@media \(max-width: 1199px\)[\s\S]*\.home-metrics[\s\S]*grid-template-columns: minmax\(0, 1fr\) minmax\(0, 1fr\)/,
    )
    expect(homeView).toMatch(
      /@media \(max-width: 759px\)[\s\S]*\.home-metrics[\s\S]*grid-template-columns: minmax\(0, 1fr\)/,
    )
    expect(homeLede).toMatch(/@media \(max-width: 759px\)/)
    expect(connectionPlaceholder).toMatch(/@media \(max-width: 759px\)/)
    expect(appShell).toMatch(
      /@media \(max-width: 1199px\)[\s\S]*\.desktop-nav,[\s\S]*display: none;[\s\S]*\.mobile-menu-trigger[\s\S]*display: inline-flex/,
    )
  })
})
