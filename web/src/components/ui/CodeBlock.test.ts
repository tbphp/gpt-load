import { mount } from '@vue/test-utils'

import CodeBlock from './CodeBlock.vue'

describe('CodeBlock', () => {
  it('renders literal code, language and a copy control without interpreting markup', () => {
    const wrapper = mount(CodeBlock, {
      props: {
        code: '<script>alert("secret")</script>',
        language: 'bash',
        copyLabel: 'Copy command',
        copySuccessLabel: 'Copied',
        copyFailureLabel: 'Copy failed',
      },
    })

    expect(wrapper.get('[data-code-language]').text()).toBe('bash')
    expect(wrapper.get('code').text()).toBe('<script>alert("secret")</script>')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.get('button').attributes('aria-label')).toBe('Copy command')
  })
})
