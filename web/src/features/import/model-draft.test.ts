import { createModelDraft, setManualModel, toGroupModels } from './model-draft'

describe('model draft', () => {
  it('turns discovered IDs into selected unique models without inventing aliases', () => {
    expect(createModelDraft(['gpt-4o', 'gpt-4o', 'claude-3'])).toEqual([
      { id: 'gpt-4o', alias: '', selected: true },
      { id: 'claude-3', alias: '', selected: true },
    ])
  })

  it('adds or updates a trimmed manual model while retaining local aliases', () => {
    const draft = createModelDraft(['gpt-4o'])
    expect(setManualModel(draft, ' custom-model ', ' local-name ')).toEqual([
      { id: 'gpt-4o', alias: '', selected: true },
      { id: 'custom-model', alias: 'local-name', selected: true },
    ])
    expect(setManualModel(draft, 'gpt-4o', 'primary')).toEqual([
      { id: 'gpt-4o', alias: 'primary', selected: true },
    ])
  })

  it('serializes only selected non-empty models for the create body', () => {
    expect(
      toGroupModels([
        { id: 'gpt-4o', alias: ' primary ', selected: true },
        { id: 'unused', alias: '', selected: false },
        { id: ' ', alias: '', selected: true },
      ]),
    ).toEqual([{ id: 'gpt-4o', alias: 'primary' }])
  })
})
