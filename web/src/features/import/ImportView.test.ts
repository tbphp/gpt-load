import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import { NetworkError } from '@/api/errors'
import { createAppRouter } from '@/app/router'
import {
  createImportOperationOwner,
  importOperationOwnerKey,
  type ImportOperationOwner,
} from '@/features/import/import-operation-owner'
import type { ImportRecoveryService } from '@/features/import/import-recovery'
import { importRecoveryKey } from '@/features/import/import-recovery'
import { createTestAppI18n as createAppI18n } from '@/test/i18n'

import ImportView from './ImportView.vue'

function recovery(overrides: Partial<ImportRecoveryService> = {}): ImportRecoveryService {
  return {
    register: () => () => {},
    captureForUnauthorized: () => 'no-active-draft',
    consume: () => null,
    clear: () => {},
    sweep: () => {},
    dispose: () => {},
    ...overrides,
  }
}

async function mountView(
  path: string,
  service = recovery(),
  operationOwner = createImportOperationOwner(),
) {
  const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(ImportView, {
    global: {
      plugins: [router, createAppI18n(undefined, 'en-US').plugin],
      provide: {
        [importRecoveryKey as symbol]: service,
        [importOperationOwnerKey as symbol]: operationOwner,
      },
      stubs: {
        NewGroupImport: {
          props: ['initialDraft'],
          template: '<div data-test="new-import">{{ initialDraft ? "recovered" : "new" }}</div>',
        },
        ExistingGroupImport: {
          name: 'ExistingGroupImport',
          props: ['initialDraft'],
          template:
            '<div data-test="existing-import">{{ initialDraft ? "recovered" : "existing" }}</div>',
        },
      },
    },
  })
  return { router, wrapper }
}

describe('ImportView', () => {
  it.each(['/import', '/import?mode=new'])('renders new-Group mode for %s', async (path) => {
    const { wrapper } = await mountView(path)
    expect(wrapper.get('[data-test="new-import"]').text()).toBe('new')
  })

  it('renders the route-backed existing selector and restores mode with browser history', async () => {
    const { router, wrapper } = await mountView('/import?mode=existing')

    expect(wrapper.get('[data-test="existing-import"]').text()).toBe('existing')
    expect(wrapper.get('[data-test="mode-existing"]').attributes()['aria-pressed']).toBe('true')

    await wrapper.get('[data-test="mode-new"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=new')
    expect(wrapper.get('[data-test="new-import"]').text()).toBe('new')

    router.back()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=existing')
    expect(wrapper.get('[data-test="existing-import"]').text()).toBe('existing')

    router.forward()
    await flushPromises()
    expect(wrapper.get('[data-test="new-import"]').text()).toBe('new')
  })

  it('retains an indeterminate operation identity across mode and page component lifecycles', async () => {
    const operationOwner: ImportOperationOwner = createImportOperationOwner()
    const operation = operationOwner.beginCreate({
      name: 'Primary',
      upstream_url: 'https://api.example.com',
      protocols: ['openai'],
      models: [{ id: 'gpt-4o', alias: '' }],
      config: {},
      keys: 'UPSTREAM_KEY_CANARY',
      confirm_same_upstream_url: false,
    })
    expect(operation).not.toBeNull()
    if (!operation) throw new Error('expected create operation')
    await operationOwner.createGroup.execute(async () => {
      throw new NetworkError()
    })

    const first = await mountView('/import?mode=existing', recovery(), operationOwner)
    await flushPromises()
    expect(first.wrapper.find('[data-test="new-import"]').exists()).toBe(true)
    expect(first.wrapper.get('[data-test="mode-existing"]').attributes()).toHaveProperty('disabled')
    expect(operationOwner.createGroup.operation.value?.idempotencyKey).toBe(
      operation.idempotencyKey,
    )
    await first.wrapper.get('[data-test="mode-existing"]').trigger('click')
    await flushPromises()
    expect(first.wrapper.find('[data-test="existing-import"]').exists()).toBe(false)
    expect(
      operationOwner.beginImportKeys({ groupID: 7, keys: 'SECOND_KEY_CANARY' }, 'existing'),
    ).toBeNull()
    expect(operationOwner.createGroup.operation.value?.idempotencyKey).toBe(
      operation.idempotencyKey,
    )
    first.wrapper.unmount()

    const second = await mountView('/import?mode=new', recovery(), operationOwner)
    expect(operationOwner.createGroup.operation.value?.idempotencyKey).toBe(
      operation.idempotencyKey,
    )
    expect(JSON.stringify(second.router.currentRoute.value)).not.toContain('UPSTREAM_KEY_CANARY')
    second.wrapper.unmount()
    operationOwner.clear()
  })

  it('consumes a 401 recovery immediately even when Login preserved an Import query variant', async () => {
    const consume = vi.fn(() => ({
      mode: 'new' as const,
      step: 1 as const,
      preset_id: 'custom' as const,
      name: '',
      upstream_url: '',
      protocols: [],
      keys: 'canary',
      header_rules: { set: {}, remove: [] },
      models: [],
    }))
    const { wrapper } = await mountView('/import?mode=existing&group_id=7', recovery({ consume }))
    expect(consume).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-test="new-import"]').text()).toBe('recovered')
  })

  it('restores an existing-mode recovery draft with raw keys kept out of the route', async () => {
    const consume = vi.fn(() => ({
      mode: 'existing' as const,
      group_id: 7,
      keys: 'RECOVERED_EXISTING_KEY_CANARY',
    }))
    const { router, wrapper } = await mountView('/import', recovery({ consume }))
    await flushPromises()

    expect(wrapper.get('[data-test="existing-import"]').text()).toBe('recovered')
    expect(wrapper.findComponent({ name: 'ExistingGroupImport' }).props('initialDraft')).toEqual({
      mode: 'existing',
      group_id: 7,
      keys: 'RECOVERED_EXISTING_KEY_CANARY',
    })
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=existing&group_id=7')
    expect(router.currentRoute.value.fullPath).not.toContain('RECOVERED_EXISTING_KEY_CANARY')
  })

  it.each(['/import?mode=bad&group_id=7', '/import?mode=existing&mode=new&group_id=7'])(
    'normalizes unknown or malformed modes to new without carrying unrelated query data',
    async (path) => {
      const { router, wrapper } = await mountView(path)
      await flushPromises()
      expect(wrapper.find('[data-test="new-import"]').exists()).toBe(true)
      expect(router.currentRoute.value.query).toEqual({ mode: 'new' })
    },
  )
})
