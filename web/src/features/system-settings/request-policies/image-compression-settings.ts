import { z } from 'zod'

export const imageCompressionPrefix = 'image_compression_setting.'

export function splitCompressionScope(value: string): string[] {
  return value.split(/[ ,\r\n]+/).filter(Boolean)
}

function readGlobCharacter(pattern: string[], index: number): number {
  if (index >= pattern.length || pattern[index] === '-' || pattern[index] === ']') {
    return -1
  }
  if (pattern[index] !== '\\') return index + 1
  return index + 1 < pattern.length ? index + 2 : -1
}

function validGlob(value: string): boolean {
  const pattern = [...value]
  for (let index = 0; index < pattern.length; index++) {
    if (pattern[index] === '\\') {
      if (++index === pattern.length) return false
      continue
    }
    if (pattern[index] !== '[') continue
    index++
    if (pattern[index] === '^') index++
    do {
      index = readGlobCharacter(pattern, index)
      if (index < 0) return false
      if (pattern[index] === '-') {
        index = readGlobCharacter(pattern, index + 1)
        if (index < 0) return false
      }
    } while (pattern[index] !== ']')
  }
  return true
}

const channelScope = z.string().refine(
  (value) =>
    value.length <= 2048 && splitCompressionScope(value).every(
      (id) => /^\+?0*[1-9]\d*$/.test(id) && BigInt(id) <= 9223372036854775807n
    ),
  { message: 'Enter positive channel IDs separated by commas.' }
)

const modelScope = z.string().refine(
  (value) => new TextEncoder().encode(value).length <= 4096 &&
    splitCompressionScope(value).every(validGlob),
  { message: 'Enter valid model globs within 4096 bytes.' }
)

export const imageCompressionSchema = z
  .object({
    enabled: z.boolean(),
    min_bytes_kb: z.number().int().min(16).max(51200),
    min_dimension: z.number().int().min(256).max(16384),
    max_dimension: z.number().int().min(256).max(8192),
    target_format: z.enum(['auto_keep_alpha', 'jpeg', 'png', 'keep_original']),
    jpeg_quality: z.number().int().min(30).max(95),
    resampler: z.enum(['catmull_rom', 'bilinear']),
    skip_animated_and_svg: z.boolean(),
    compress_remote_urls: z.boolean(),
    remote_timeout_ms: z.number().int().min(500).max(15000),
    remote_max_download_mb: z.number().int().min(1).max(50),
    max_process_images: z.number().int().min(1).max(64),
    per_request_budget_ms: z.number().int().min(500).max(20000),
    failure_policy: z.enum(['keep_original', 'reject_request']),
    channel_ids: channelScope,
    model_patterns: modelScope,
    exclude_channel_ids: channelScope,
    exclude_model_patterns: modelScope,
    threshold_enabled: z.boolean(),
  })
  .refine((settings) => !settings.threshold_enabled || settings.max_dimension <= settings.min_dimension, {
    path: ['max_dimension'],
    message: 'The target edge cannot exceed the dimension threshold.',
  })

export type ImageCompressionSettings = z.infer<typeof imageCompressionSchema>

export const imageCompressionDefaults: ImageCompressionSettings = {
  enabled: false,
  min_bytes_kb: 512,
  min_dimension: 2048,
  max_dimension: 2048,
  target_format: 'auto_keep_alpha',
  jpeg_quality: 82,
  resampler: 'catmull_rom',
  skip_animated_and_svg: true,
  compress_remote_urls: false,
  remote_timeout_ms: 3000,
  remote_max_download_mb: 15,
  max_process_images: 12,
  per_request_budget_ms: 4000,
  failure_policy: 'keep_original',
  channel_ids: '',
  model_patterns: '',
  exclude_channel_ids: '',
  exclude_model_patterns: '',
  threshold_enabled: true,
}

export function readImageCompressionSettings(
  options: Record<string, unknown>
): ImageCompressionSettings {
  const candidate = Object.fromEntries(
    Object.entries(imageCompressionDefaults).map(([name, fallback]) => {
      const value = options[imageCompressionPrefix + name]
      if (typeof value !== 'string' || typeof fallback === 'string') {
        return [name, value ?? fallback]
      }
      return [
        name,
        typeof fallback === 'boolean' ? value === 'true' : Number(value),
      ]
    })
  )
  const result = imageCompressionSchema.safeParse(candidate)
  return result.success ? result.data : imageCompressionDefaults
}
