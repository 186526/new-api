/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { z } from 'zod'

export const GROUP_POLICIES_OPTION = 'websearch_setting.group_policies'

export const TOOL_ENDPOINTS = {
  'tools/search': '/v1/tools/search',
  'tools/fetch': '/v1/tools/fetch',
  'alpha/search': '/v1/alpha/search',
  'moonshot/search': '/moonshot.service/v1/search',
  'moonshot/fetch': '/moonshot.service/v1/fetch',
} as const

export type ToolContract = keyof typeof TOOL_ENDPOINTS
export const TOOL_CONTRACTS = Object.keys(TOOL_ENDPOINTS) as ToolContract[]
export const SEARCH_CONTRACTS: ToolContract[] = [
  'tools/search',
  'alpha/search',
  'moonshot/search',
]
export const FETCH_CONTRACTS: ToolContract[] = [
  'tools/fetch',
  'moonshot/fetch',
]

export const TOOL_PROVIDERS = [
  'antigravity',
  'codex',
  'deepseek',
  'exa',
  'local',
]

export const TOOL_POLICY_MODES = [
  'inherit',
  'disabled',
  'prefer',
  'only',
] as const
const policyMode = z.enum(TOOL_POLICY_MODES)

export const TOOL_POLICY_LABELS = {
  inherit: 'toolsApi.inherit',
  disabled: 'Disabled',
  prefer: 'toolsApi.prefer',
  only: 'Only listed providers',
} as const

export type ToolProviderCapability = {
  provider: string
  contracts: readonly ToolContract[]
}

export function supportsToolContract(
  provider: string,
  contract?: ToolContract,
  directory?: readonly ToolProviderCapability[]
) {
  if (contract?.endsWith('/search') && provider === 'local') return false
  if (!directory) return true
  return directory.some(
    (entry) =>
      entry.provider === provider &&
      (!contract || entry.contracts.includes(contract))
  )
}

export function usesProviderOrder(mode: z.infer<typeof policyMode>) {
  return mode === 'prefer' || mode === 'only'
}
const groupPolicy = z.record(
  z.string(),
  z.object({ mode: policyMode, providers: z.array(z.string()).nullish() })
)
const groupPolicies = z.record(z.string(), groupPolicy)

type GroupPolicies = z.infer<typeof groupPolicies>

export function readGroupPolicies(
  value: string | undefined
): GroupPolicies | null {
  if (!value?.trim()) return {}

  try {
    const parsed: unknown = JSON.parse(value)
    const result = groupPolicies.safeParse(parsed ?? {})
    if (result.success) return result.data
  } catch {
    return null
  }
  return null
}

export function parseProviderOrder(value: string): string[] {
  return value
    .toLowerCase()
    .split(',')
    .map((name) => name.trim())
    .filter(Boolean)
}

export function normalizeProviderOrder(providers: string[], _contract?: ToolContract): string[] {
  return providers
}

export function createGroupPolicyFormSchema(
  t: (key: string) => string,
  directory?: readonly ToolProviderCapability[]
) {
  return z
    .object({
      rules: z.record(
        z.enum(TOOL_CONTRACTS),
        z.object({ mode: policyMode, order: z.string() })
      ),
    })
    .superRefine((value, ctx) => {
      for (const contract of TOOL_CONTRACTS) {
        const rule = value.rules[contract]
        if (!usesProviderOrder(rule.mode)) continue
        const providers = parseProviderOrder(rule.order)
        const unsupported = providers.find(
          (provider) => !supportsToolContract(provider, contract, directory)
        )
        let message: string | undefined
        if (!providers.length) {
          message = t('toolsApi.providerRequired')
        } else if (unsupported) {
          message = `${t('Provider unavailable for this operation')}: ${unsupported}`
        } else if (new Set(providers).size !== providers.length) {
          message = t('Provider order cannot contain duplicates')
        }
        if (message) {
          ctx.addIssue({
            code: 'custom',
            path: ['rules', contract, 'order'],
            message,
          })
        }
      }
    })
}

export type GroupPolicyFormValues = z.infer<
  ReturnType<typeof createGroupPolicyFormSchema>
>

export function groupPolicyFormValues(
  policies: GroupPolicies,
  group: string
): GroupPolicyFormValues {
  const groupRules = Object.hasOwn(policies, group)
    ? policies[group]
    : undefined
  const rules = Object.fromEntries(
    TOOL_CONTRACTS.map((contract) => {
      const policy = groupRules?.[contract]
      return [
        contract,
        {
          mode: policy?.mode ?? 'inherit',
          order:
            policy && usesProviderOrder(policy.mode)
              ? normalizeProviderOrder(policy.providers ?? [], contract).join(', ')
              : '',
        },
      ]
    })
  ) as GroupPolicyFormValues['rules']
  return { rules }
}

export function updateGroupPolicies(
  policies: GroupPolicies,
  group: string,
  values: GroupPolicyFormValues,
  dirtyRules: Partial<
    Record<ToolContract, Partial<Record<'mode' | 'order', boolean>>>
  >
): GroupPolicies {
  const nextPolicies = { ...policies }
  const groupRules = Object.hasOwn(policies, group)
    ? { ...policies[group] }
    : {}

  for (const contract of TOOL_CONTRACTS) {
    if (!dirtyRules[contract]) continue
    const rule = values.rules[contract]
    if (rule.mode === 'inherit') {
      delete groupRules[contract]
    } else if (rule.mode === 'disabled') {
      groupRules[contract] = { mode: 'disabled' }
    } else {
      groupRules[contract] = {
        mode: rule.mode,
        providers: normalizeProviderOrder(parseProviderOrder(rule.order), contract),
      }
    }
  }

  if (Object.keys(groupRules).length === 0) delete nextPolicies[group]
  else return { ...nextPolicies, [group]: groupRules }
  return nextPolicies
}

export function getGroupPoliciesOption(
  options: { key: string; value: string }[] | undefined
) {
  return readGroupPolicies(
    options?.find((option) => option.key === GROUP_POLICIES_OPTION)?.value
  )
}

export type { GroupPolicies }
