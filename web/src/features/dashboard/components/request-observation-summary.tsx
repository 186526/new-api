import { useTranslation } from 'react-i18next'

import type { RequestObservations } from '@/features/performance-metrics/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export function RequestObservationSummary(props: {
  data?: RequestObservations
}) {
  const { t, i18n } = useTranslation()
  const role = useAuthStore((state) => state.auth.user?.role)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  if (!props.data || role == null || role < ROLE.ADMIN) return null

  const rows = [
    [t('Tool requests'), props.data.tool_requests],
    [t('Tool cache hits'), props.data.tool_cache_hits],
    [t('Refunds requested'), props.data.refund_requested],
    [t('Refunds completed'), props.data.refund_completed],
    [t('Refunds failed'), props.data.refund_failed ?? 0],
    [
      t('Images preprocessed'),
      props.data.image_preprocessing_runs
        ? props.data.images_preprocessed
        : undefined,
    ],
    [
      t('Images compressed'),
      props.data.image_count_samples ? props.data.images_compressed : undefined,
    ],
    [
      t('Image payload bytes saved'),
      props.data.image_savings_samples
        ? props.data.image_bytes_saved
        : undefined,
    ],
    [t('Image savings unconfirmed'), props.data.image_savings_unknown],
    [t('Redacted requests'), props.data.redacted_requests],
    [t('Redaction matches'), props.data.redaction_matches],
    [t('Automatic reasoning requests'), props.data.automatic_reasoning],
    [
      t('Reasoning fields sent'),
      props.data.reasoning_observed
        ? props.data.reasoning_fields_present
        : undefined,
    ],
    [
      t('Outbound reasoning fields not confirmed'),
      props.data.reasoning_outbound_unknown,
    ],
  ] as const
  return (
    <dl
      aria-label={t('Request observations')}
      className='grid grid-cols-2 gap-2 text-xs sm:grid-cols-3'
    >
      {rows.map(([label, value]) => (
        <div key={label} className='flex min-w-0 flex-col gap-1'>
          <dt className='text-muted-foreground'>{label}</dt>
          <dd className='font-medium tabular-nums'>
            {value == null ? t('Unavailable') : formatNumber(value, locale)}
          </dd>
        </div>
      ))}
    </dl>
  )
}
