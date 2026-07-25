import baseStyles from '@/styles/base.css?raw'

import appSelect from './AppSelect.vue?raw'
import queryFeedback from './QueryFeedback.vue?raw'

describe('shared accessibility styles', () => {
  it('keeps mobile text inputs at 16px and recovery controls at 44px without spinner motion', () => {
    expect(baseStyles).toMatch(/@media \(max-width: 640px\) \{[\s\S]*body \{\s*font-size: 1rem;/)
    expect(baseStyles).toMatch(/input:not\(\[type=['"](?:checkbox|radio)['"]\]\).*font-size: 1rem/s)
    expect(queryFeedback).toMatch(/min-height: 44px/)
    expect(queryFeedback).toMatch(/prefers-reduced-motion: reduce[\s\S]*animation: none/)
    expect(appSelect).toMatch(/\.app-select__item[\s\S]*min-height: 44px/)
  })
})
