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

// ============================================================================
// API Key Schema & Types
// ============================================================================

export const redactionRuleIDs = [
  'email',
  'phone',
  'cn_id',
  'credit_card',
  'private_ip',
  'all_ip',
  'api_keys',
  'cloud_tokens',
  'dsn_secret',
  'jwt_bearer',
  'pem_private_key',
] as const

export const redactionConfigSchema = z.object({
  strategy: z.enum(['typed_placeholder', 'mask', 'hmac_short']),
  builtin_rules: z.array(z.enum(redactionRuleIDs)),
  scopes: z.object({
    user_messages: z.boolean(),
    tool_io: z.boolean(),
    system_prompt: z.boolean(),
  }),
  allowlist: z.array(z.string().max(128)).max(50),
  custom_rules: z
    .array(
      z.object({
        id: z.string().min(1).max(64),
        label: z.string().regex(/^[A-Z0-9_]{2,24}$/),
        pattern: z.string().min(1).max(256),
        replacement: z.string().max(128).optional(),
      })
    )
    .max(10),
  restore_pii_in_response: z.boolean(),
})

export type RedactionConfig = z.output<typeof redactionConfigSchema>

export const apiKeySchema = z.object({
  redaction_enabled: z.boolean().optional(),
  redaction_revision: z.number().int().nonnegative().optional(),
  redaction_summary: z.object({
    builtin_rules: z.number(),
    custom_rules: z.number(),
    scopes: z.object({
      user_messages: z.boolean(),
      tool_io: z.boolean(),
      system_prompt: z.boolean(),
    }),
  }).optional(),
  redaction_config: redactionConfigSchema.optional(),
  id: z.number(),
  name: z.string(),
  key: z.string(),
  status: z.number(), // 1: enabled, 2: disabled, 3: expired, 4: exhausted
  remain_quota: z.number(),
  used_quota: z.number(),
  unlimited_quota: z.boolean(),
  expired_time: z.number(), // -1 for never expires
  created_time: z.number(),
  accessed_time: z.number(),
  group: z.string().nullish().default(''),
  auto_groups: z.array(z.string()).nullish().default(null),
  cross_group_retry: z
    .preprocess((v) => {
      if (v === 1) return true
      if (v === 0) return false
      return v
    }, z.boolean())
    .optional()
    .default(false),
  model_limits_enabled: z.boolean(),
  model_limits: z.string().nullish().default(''),
  allow_ips: z.string().nullish().default(''),
  allow_user_quota_query: z.boolean().optional().default(false),
})

export type ApiKey = z.infer<typeof apiKeySchema>

// ============================================================================
// API Request/Response Types
// ============================================================================

export interface ApiResponse<T = unknown> {
  success: boolean
  field?: string
  message?: string
  message_key?: string
  message_params?: Record<string, unknown>
  data?: T
}

export interface GetApiKeysParams {
  p?: number
  size?: number
}

export interface GetApiKeysResponse {
  success: boolean
  message?: string
  data?: {
    items: ApiKey[]
    total: number
    page: number
    page_size: number
  }
}

export interface SearchApiKeysParams {
  keyword?: string
  token?: string
  p?: number
  size?: number
}

export interface ApiKeyFormData {
  redaction_enabled?: boolean
  redaction_config?: RedactionConfig
  name: string
  remain_quota: number
  expired_time: number
  unlimited_quota: boolean
  model_limits_enabled: boolean
  model_limits: string
  allow_ips: string
  allow_user_quota_query: boolean
  group: string
  auto_groups: string[]
  cross_group_retry: boolean
}

export interface TokenAutoGroupsConfig {
  groups: string[]
  max_count: number
}

// ============================================================================
// Dialog Types
// ============================================================================

export type ApiKeysDialogType =
  | 'create'
  | 'update'
  | 'delete'
  | 'batch-delete'
  | 'cc-switch'
