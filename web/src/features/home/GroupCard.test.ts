import { mount } from '@vue/test-utils'

import type { GroupSummary } from '@/api/control/types'
import type { HealthGroupDto } from '@/app/resources/health'
import { createTestAppI18n } from '@/test/i18n'

import GroupCard from './GroupCard.vue'

const longName = `Primary-${'segment-'.repeat(20)}Group`
const group: GroupSummary = {
  id: 9,
  name: longName,
  upstream_url: `https://${'long-host-'.repeat(12)}example.com/v1`,
  protocols: ['openai-chat-completions', 'anthropic'],
  models: [],
  enabled: true,
  key_count: 2,
}
const health: HealthGroupDto = {
  id: 9,
  name: longName,
  enabled: true,
  counts: {
    total: 2,
    available: 0,
    cooldown: 2,
    blacklisted: 0,
    disabled: 0,
  },
}

function mountCard(groupValue = group, healthValue: HealthGroupDto | undefined = health) {
  const appI18n = createTestAppI18n(undefined, 'en-US')
  return mount(GroupCard, {
    props: {
      group: groupValue,
      health: healthValue,
    },
    global: {
      plugins: [appI18n.plugin],
      stubs: {
        RouterLink: {
          props: ['to'],
          template: '<a :href="to"><slot /></a>',
        },
      },
    },
  })
}

describe('GroupCard', () => {
  it('preserves long identity text and translates protocol labels', () => {
    const wrapper = mountCard()

    expect(wrapper.get('h3').text()).toBe(longName)
    expect(wrapper.text()).toContain('long-host-')
    expect(wrapper.text()).toContain('OpenAI')
    expect(wrapper.text()).toContain('Anthropic')
    expect(wrapper.get('[href="/groups/9?tab=keys"]').text()).toContain('View details')
    expect(wrapper.get('[href="/import?mode=existing&group_id=9"]').text()).toContain('Import keys')
  })

  it('explains no-model and no-available-key serviceability separately', async () => {
    const wrapper = mountCard()
    expect(wrapper.get('[data-test="group-service-reason"]').text()).toContain('no models')

    await wrapper.setProps({
      group: {
        ...group,
        models: [{ id: 'gpt-real', alias: '' }],
      },
    })

    expect(wrapper.get('[data-test="group-service-reason"]').text()).toMatch(
      /no upstream key is currently available/i,
    )
  })

  it('shows a zero-model Responses Group as resource-only serviceable', () => {
    const wrapper = mountCard(
      {
        ...group,
        protocols: ['openai-responses'],
        models: [],
      },
      {
        ...health,
        counts: {
          ...health.counts,
          available: 1,
          cooldown: 1,
        },
      },
    )

    expect(wrapper.text()).toContain('Serviceable')
    expect(wrapper.get('[data-test="group-service-reason"]').text()).toMatch(
      /responses resource endpoints/i,
    )
    expect(wrapper.text()).not.toContain('This Group has no models configured.')
  })
})
