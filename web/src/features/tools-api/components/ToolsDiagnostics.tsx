import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { getProviderSummary, summaryKey } from '../lib/admin'

export function ToolsDiagnostics() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const summary = useQuery({
    queryKey: summaryKey,
    queryFn: getProviderSummary,
    meta: { errorToast: false },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Tool provider readiness')}</CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        <Button
          type='button'
          variant='outline'
          className='self-start'
          disabled={summary.isFetching}
          onClick={() => void summary.refetch()}
        >
          {t('Refresh')}
        </Button>
        {summary.isPending && <p role='status'>{t('Loading...')}</p>}
        {summary.isError && (
          <p role='status'>{t('Tool supply information is unavailable.')}</p>
        )}
        {summary.data?.length === 0 && <p>{t('No tool providers')}</p>}
        {!!summary.data?.length && (
          <ul className='flex flex-col gap-2'>
            {summary.data.map((item) => (
              <li
                className='flex flex-wrap items-center gap-2'
                key={item.provider}
              >
                <strong>{item.provider}</strong>
                <Badge variant='outline'>
                  {t('Channels: {{countText}}', {
                    countText: formatNumber(item.available_channels, locale),
                  })}
                </Badge>
                {item.missing_mapping_channels > 0 && (
                  <Badge variant='secondary'>
                    {t('Missing mappings: {{countText}}', {
                      countText: formatNumber(
                        item.missing_mapping_channels,
                        locale
                      ),
                    })}
                  </Badge>
                )}
                {item.global_credential_configured && (
                  <Badge variant='secondary'>
                    {t('Global key configured')}
                  </Badge>
                )}
                {item.local_fallback && (
                  <Badge variant='secondary'>
                    {t('Built-in fetch fallback')}
                  </Badge>
                )}
                {item.contracts.map((contract) => {
                  const detail = item.contract_details?.find((entry) => entry.contract === contract)
                  const available = detail?.available_channels
                  const unmapped = detail?.missing_mapping_channels
                  return (
                    <Badge key={contract} variant='outline'>
                      {contract}
                      {item.native_contracts.includes(contract) && ` · ${t('Native')}`}
                      {available != null && ` · ${t('Channels: {{countText}}', { countText: formatNumber(available, locale) })}`}
                      {unmapped != null && unmapped > 0 && ` · ${t('Missing mappings: {{countText}}', { countText: formatNumber(unmapped, locale) })}`}
                    </Badge>
                  )
                })}
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
