import { describe, expect, it } from 'vitest'

import {
  imageCompressionDefaults,
  imageCompressionSchema,
  readImageCompressionSettings,
} from '../image-compression-settings'

describe('image compression settings', () => {
  it('hydrates saved scope and numeric values while keeping compression disabled by default', () => {
    expect(
      readImageCompressionSettings({
        'image_compression_setting.channel_ids': '12, 14',
        'image_compression_setting.min_bytes_kb': '2048',
      })
    ).toMatchObject({
      enabled: false,
      channel_ids: '12, 14',
      min_bytes_kb: 2048,
    })
  })

  it('rejects a target edge above the dimension threshold at the target field', () => {
    const policy = imageCompressionSchema.safeParse({
      ...imageCompressionDefaults,
      max_dimension: 4096,
    })
    expect(policy.error?.issues[0]?.path).toEqual(['max_dimension'])
  })

  it('allows the target edge above an inactive threshold', () => {
    expect(imageCompressionSchema.safeParse({
      ...imageCompressionDefaults,
      threshold_enabled: false,
      max_dimension: 4096,
    }).success).toBe(true)
  })

  it('rejects malformed excluded model globs', () => {
    const result = imageCompressionSchema.safeParse({
      ...imageCompressionDefaults,
      exclude_model_patterns: 'gpt-[1-3],gemini-[',
    })
    expect(result.error?.issues[0]?.path).toEqual(['exclude_model_patterns'])
  })

  it('rejects nonpositive and nonnumeric channel IDs', () => {
    for (const channel_ids of ['0', '-12', '1, admin']) {
      expect(
        imageCompressionSchema.safeParse({
          ...imageCompressionDefaults,
          channel_ids,
        }).success
      ).toBe(false)
    }
  })
})
