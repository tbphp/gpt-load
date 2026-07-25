import { QueryClient } from '@tanstack/vue-query'

import { mountApp } from '@/test/mount-app'

import ModelDraftEditor from './ModelDraftEditor.vue'

describe('ModelDraftEditor', () => {
  it('edits selection and aliases while preserving caller metadata', async () => {
    const mounted = await mountApp(ModelDraftEditor, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: {
        props: {
          modelValue: [
            {
              id: 'old',
              alias: 'public',
              selected: true,
              origin: 'persisted',
              rediscovered: false,
            },
          ],
        },
      },
    })

    await mounted.wrapper.get('[data-test="model-selected-0"]').setValue(false)
    expect(mounted.wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([
      {
        id: 'old',
        alias: 'public',
        selected: false,
        origin: 'persisted',
        rediscovered: false,
      },
    ])

    await mounted.wrapper.setProps({
      modelValue: [
        {
          id: 'old',
          alias: 'public',
          selected: true,
          origin: 'persisted',
          rediscovered: false,
        },
      ],
    })
    await mounted.wrapper.get('[data-test="model-alias-0"]').setValue('renamed')
    expect(mounted.wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([
      {
        id: 'old',
        alias: 'renamed',
        selected: true,
        origin: 'persisted',
        rediscovered: false,
      },
    ])

    mounted.wrapper.unmount()
  })

  it('keeps alias input focus while the parent accepts an edited alias', async () => {
    const mounted = await mountApp(ModelDraftEditor, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: {
        props: { modelValue: [{ id: 'provider', alias: 'public-a', selected: true }] },
        attachTo: document.body,
      },
    })
    const alias = mounted.wrapper.get('[data-test="model-alias-0"]')
    ;(alias.element as HTMLInputElement).focus()
    await alias.setValue('public-b')
    await mounted.wrapper.setProps({
      modelValue: [{ id: 'provider', alias: 'public-b', selected: true }],
    })

    expect(document.activeElement).toBe(mounted.wrapper.get('[data-test="model-alias-0"]').element)
    mounted.wrapper.unmount()
  })

  it('appends a different alias for the same model ID instead of overwriting the existing row', async () => {
    const mounted = await mountApp(ModelDraftEditor, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: {
        props: {
          modelValue: [{ id: 'provider', alias: 'public-a', selected: true }],
        },
      },
    })

    await mounted.wrapper.get('[data-test="manual-model-id"]').setValue('provider')
    await mounted.wrapper.get('[data-test="manual-model-alias"]').setValue('public-b')
    await mounted.wrapper.get('[data-test="add-manual-model"]').trigger('click')

    expect(mounted.wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([
      { id: 'provider', alias: 'public-a', selected: true },
      { id: 'provider', alias: 'public-b', selected: true },
    ])
    mounted.wrapper.unmount()
  })

  it('reselects an existing exact model ID and alias pair without appending a duplicate', async () => {
    const mounted = await mountApp(ModelDraftEditor, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: {
        props: {
          modelValue: [{ id: 'provider', alias: 'public-a', selected: false }],
        },
      },
    })

    await mounted.wrapper.get('[data-test="manual-model-id"]').setValue('provider')
    await mounted.wrapper.get('[data-test="manual-model-alias"]').setValue('public-a')
    await mounted.wrapper.get('[data-test="add-manual-model"]').trigger('click')

    expect(mounted.wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([
      { id: 'provider', alias: 'public-a', selected: true },
    ])
    mounted.wrapper.unmount()
  })

  it('preserves multiple saved aliases when manually adding another alias for the same model ID', async () => {
    const mounted = await mountApp(ModelDraftEditor, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: {
        props: {
          modelValue: [
            { id: 'provider', alias: 'public-a', selected: true },
            { id: 'provider', alias: 'public-b', selected: true },
          ],
        },
      },
    })

    await mounted.wrapper.get('[data-test="manual-model-id"]').setValue('provider')
    await mounted.wrapper.get('[data-test="manual-model-alias"]').setValue('public-c')
    await mounted.wrapper.get('[data-test="add-manual-model"]').trigger('click')

    expect(mounted.wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([
      { id: 'provider', alias: 'public-a', selected: true },
      { id: 'provider', alias: 'public-b', selected: true },
      { id: 'provider', alias: 'public-c', selected: true },
    ])
    mounted.wrapper.unmount()
  })

  it('disables every mutable ModelDraft subcontrol', async () => {
    const mounted = await mountApp(ModelDraftEditor, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: {
        props: { modelValue: [{ id: 'old', alias: 'public', selected: true }], disabled: true },
      },
    })
    for (const selector of [
      '[data-test="model-selected-0"]',
      '[data-test="model-alias-0"]',
      '[data-test="manual-model-id"]',
      '[data-test="manual-model-alias"]',
      '[data-test="add-manual-model"]',
    ]) {
      expect(mounted.wrapper.get(selector).attributes()).toHaveProperty('disabled')
    }
    mounted.wrapper.unmount()
  })

  it('adds a trimmed manual model and alias after discovery failure', async () => {
    const mounted = await mountApp(ModelDraftEditor, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: { props: { modelValue: [] } },
    })

    await mounted.wrapper.get('[data-test="manual-model-id"]').setValue(' manual-model ')
    await mounted.wrapper.get('[data-test="manual-model-alias"]').setValue(' manual-public ')
    await mounted.wrapper.get('[data-test="add-manual-model"]').trigger('click')

    expect(mounted.wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([
      { id: 'manual-model', alias: 'manual-public', selected: true },
    ])
    mounted.wrapper.unmount()
  })
})
