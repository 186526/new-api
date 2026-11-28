import { describe, expect, it } from 'vitest'

import {
  autoReasoningFormSchema,
  defaultAutoReasoningPolicy,
  policyToForm,
  previewEffort,
} from '../auto-reasoning-policy'

describe('automatic reasoning policy', () => {
  it('serializes the current multiline draft into trimmed entries', () => {
    const draft = policyToForm(defaultAutoReasoningPolicy)
    draft.groups = ' default \n\nagent\n'
    draft.model_patterns = 're:^gpt-5\\b\n'
    const policy = autoReasoningFormSchema.parse(draft)
    expect(policy.groups).toEqual(['default', 'agent'])
    expect(policy.model_patterns).toEqual(['re:^gpt-5\\b'])
    expect(policyToForm(policy).groups).toBe('default\nagent')
  })

  it('rejects duplicate threshold values on the dependent field', () => {
    const draft = policyToForm({
      ...defaultAutoReasoningPolicy,
      tiers: [
        { max_scale: 1500, effort: 'low' },
        { max_scale: 1500, effort: 'medium' },
      ],
    })
    const parsed = autoReasoningFormSchema.safeParse(draft)
    expect(parsed.error?.issues).toEqual([
      expect.objectContaining({ path: ['tiers', 1, 'max_scale'] }),
    ])
  })

  it('uses the next tier only after passing the inclusive upper bound', () => {
    const { tiers, overflow_effort } = defaultAutoReasoningPolicy
    expect(previewEffort(tiers, overflow_effort, 1500)).toBe('low')
    expect(previewEffort(tiers, overflow_effort, 1501)).toBe('medium')
    expect(previewEffort(tiers, 'xhigh', 32001)).toBe('xhigh')
  })
})
