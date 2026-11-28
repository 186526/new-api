import * as z from 'zod'

export const AUTO_EFFORT_KEY = 'reasoning_setting.auto_effort_policy'
export const efforts = [
  'none',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
] as const
const effort = z.enum(efforts)
export const scaleMetrics = ['estimated_tokens', 'input_chars'] as const
export const conflictPolicies = [
  'only_when_unset',
  'cap_to_auto',
  'always_override',
] as const
const entries = z
  .array(
    z
      .string()
      .min(1)
      .refine((text) => new TextEncoder().encode(text).length <= 256)
  )
  .max(32)
  .refine((values) => new Set(values).size === values.length)

export const autoReasoningSchema = z
  .object({
    enabled: z.boolean(),
    scale_metric: z.enum(scaleMetrics),
    conflict_policy: z.enum(conflictPolicies),
    respect_explicit_disable: z.boolean(),
    tiers: z
      .array(
        z.object({
          max_scale: z.number().int().positive().max(1_000_000_000),
          effort,
        })
      )
      .min(1)
      .max(6),
    overflow_effort: effort,
    groups: entries,
    model_patterns: entries,
    exclude_model_patterns: entries,
  })
  .superRefine((value, ctx) => {
    value.tiers.forEach((tier, index) => {
      if (index > 0 && value.tiers[index - 1].max_scale >= tier.max_scale) {
        ctx.addIssue({
          code: 'custom',
          path: ['tiers', index, 'max_scale'],
          message: 'Thresholds must increase strictly',
        })
      }
    })
  })

export type AutoReasoningPolicy = z.infer<typeof autoReasoningSchema>

const lines = z.string().transform((text) =>
  text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
)
export const autoReasoningFormSchema = z
  .object({
    ...autoReasoningSchema.shape,
    groups: lines,
    model_patterns: lines,
    exclude_model_patterns: lines,
  })
  .pipe(autoReasoningSchema)
export type AutoReasoningForm = z.input<typeof autoReasoningFormSchema>

export function policyToForm(policy: AutoReasoningPolicy): AutoReasoningForm {
  return {
    ...policy,
    groups: policy.groups.join('\n'),
    model_patterns: policy.model_patterns.join('\n'),
    exclude_model_patterns: policy.exclude_model_patterns.join('\n'),
  }
}
export const defaultAutoReasoningPolicy: AutoReasoningPolicy = {
  enabled: false,
  scale_metric: 'estimated_tokens',
  conflict_policy: 'only_when_unset',
  respect_explicit_disable: true,
  tiers: [
    { max_scale: 1500, effort: 'low' },
    { max_scale: 8000, effort: 'medium' },
    { max_scale: 32000, effort: 'high' },
  ],
  overflow_effort: 'high',
  groups: [],
  model_patterns: [],
  exclude_model_patterns: [],
}

export function parseAutoReasoningPolicy(
  raw: string
): AutoReasoningPolicy | undefined {
  try {
    const value = JSON.parse(raw) as unknown
    if (!value || typeof value !== 'object' || Array.isArray(value)) {
      return undefined
    }
    const result = autoReasoningSchema.safeParse({
      ...defaultAutoReasoningPolicy,
      ...value,
    })
    if (result.success) return result.data
  } catch {
    return undefined
  }
}

export function previewEffort(
  tiers: AutoReasoningPolicy['tiers'],
  overflow: AutoReasoningPolicy['overflow_effort'],
  scale: number
) {
  return tiers.find((tier) => scale <= tier.max_scale)?.effort ?? overflow
}

export function previewDraftDecision(
  policy: AutoReasoningPolicy,
  scale: number,
  explicit: '' | AutoReasoningPolicy['overflow_effort']
) {
  const tier = policy.tiers.findIndex((rule) => scale <= rule.max_scale) + 1
  const target = previewEffort(policy.tiers, policy.overflow_effort, scale)
  if (explicit === 'none' && policy.respect_explicit_disable) {
    return { tier, target, effort: explicit, reason: 'explicit_disable' }
  }
  if (explicit && policy.conflict_policy === 'only_when_unset') {
    return { tier, target, effort: explicit, reason: 'explicit_strength' }
  }
  if (explicit && policy.conflict_policy === 'cap_to_auto') {
    if (efforts.indexOf(explicit) <= efforts.indexOf(target)) {
      return { tier, target, effort: explicit, reason: 'within_cap' }
    }
    return { tier, target, effort: target, reason: 'capped' }
  }
  return { tier, target, effort: target, reason: explicit ? 'overridden' : 'applied' }
}
