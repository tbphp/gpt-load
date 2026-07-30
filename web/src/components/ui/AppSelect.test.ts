import { mount } from '@vue/test-utils'

import AppSelect from './AppSelect.vue'
import appSelectSource from './AppSelect.vue?raw'

function readAppSelectStyle(
  selector: string,
  properties: readonly string[],
): Record<string, string> {
  const styleSource = appSelectSource.match(/<style>([\s\S]*?)<\/style>/)?.[1]
  if (styleSource === undefined) throw new Error('AppSelect styles are missing')

  const styleElement = document.createElement('style')
  styleElement.textContent = styleSource
  document.head.append(styleElement)
  const styleRule = Array.from(styleElement.sheet?.cssRules ?? []).find(
    (rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && rule.selectorText === selector,
  )
  if (styleRule === undefined) throw new Error(`Missing style rule: ${selector}`)

  const declarations = Object.fromEntries(
    properties.map((property) => [property, styleRule.style.getPropertyValue(property)]),
  )
  styleElement.remove()
  return declarations
}

describe('AppSelect', () => {
  it('forwards control accessibility attributes to the combobox trigger', () => {
    const wrapper = mount(AppSelect, {
      props: {
        id: 'protocol',
        label: 'Protocol',
        modelValue: 'openai-chat-completions',
        options: [{ value: 'openai-chat-completions', label: 'OpenAI' }],
        'aria-describedby': 'protocol-help protocol-error',
        'aria-invalid': 'true',
      },
    })

    const combobox = wrapper.get('[role="combobox"]')
    expect(combobox.attributes('id')).toBe('protocol')
    expect(combobox.attributes('aria-describedby')).toBe('protocol-help protocol-error')
    expect(combobox.attributes('aria-invalid')).toBe('true')
  })

  it('caps portaled content to the available viewport width and wraps long option labels', () => {
    expect(readAppSelectStyle('.app-select__content', ['max-width'])).toEqual({
      'max-width': 'var(--reka-select-content-available-width)',
    })
    expect(
      readAppSelectStyle('.app-select__item', [
        'min-width',
        'max-width',
        'white-space',
        'overflow-wrap',
      ]),
    ).toEqual({
      'min-width': '0',
      'max-width': '100%',
      'white-space': 'normal',
      'overflow-wrap': 'anywhere',
    })
  })

  it('lets the selected long label shrink and wrap without collapsing the chevron', () => {
    const longLabel = 'AccessKeyWithAnIntentionallyLongUnbrokenName'
    const wrapper = mount(AppSelect, {
      props: {
        label: 'AccessKey',
        modelValue: '1',
        options: [{ value: '1', label: longLabel }],
      },
    })

    expect(wrapper.find('.app-select__value').exists()).toBe(true)
    expect(wrapper.get('.app-select__chevron').attributes('aria-hidden')).toBe('true')
    expect(
      readAppSelectStyle('.app-select__value', ['min-width', 'white-space', 'overflow-wrap']),
    ).toEqual({
      'min-width': '0',
      'white-space': 'normal',
      'overflow-wrap': 'anywhere',
    })
    expect(readAppSelectStyle('.app-select__chevron', ['flex-shrink'])).toEqual({
      'flex-shrink': '0',
    })
  })
})
