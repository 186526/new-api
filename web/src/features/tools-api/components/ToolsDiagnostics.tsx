import { useQuery } from '@tanstack/react-query'
import { ChevronDown, Globe, RefreshCw, Search } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getProviderSummary, summaryKey, type ProviderSummary } from '../lib/admin'

function isConfiguredProvider(item: ProviderSummary) {
  return (
    item.available_channels > 0 ||
    item.missing_mapping_channels > 0 ||
    item.global_credential_configured ||
    item.local_fallback
  )
}

export function ToolsDiagnostics() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [detailsOpen, setDetailsOpen] = useState(false)
  const [showUnconfigured, setShowUnconfigured] = useState(false)
  const summary = useQuery({
    queryKey: summaryKey,
    queryFn: getProviderSummary,
    meta: { errorToast: false },
  })

  const items = summary.data ?? []
  const searchProviders = items.filter((item) =>
    (item.contracts ?? []).some((contract) => contract.endsWith('/search'))
  )
  const fetchProviders = items.filter((item) =>
    (item.contracts ?? []).some((contract) => contract.endsWith('/fetch'))
  )
  const searchChannels = searchProviders.reduce(
    (sum, item) => sum + item.available_channels,
    0
  )
  const missingMappings = items.reduce(
    (sum, item) => sum + item.missing_mapping_channels,
    0
  )
  const globalExaReady = items.some((item) => item.global_credential_configured)
  const activeSearchProviders = searchProviders.filter(isConfiguredProvider)
  const activeFetchProviders = fetchProviders.filter(
    (item) => isConfiguredProvider(item) && item.provider !== 'local'
  )
  const configuredItems = items.filter(isConfiguredProvider)
  const hiddenCount = Math.max(0, items.length - configuredItems.length)
  const visibleRows = showUnconfigured ? items : configuredItems

  return (
    <Card>
      <CardContent className='flex flex-col gap-3 p-4'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <div className='flex items-center gap-2'>
            <h3 className='text-sm font-medium'>{t('Tool provider readiness')}</h3>
            {summary.isPending && (
              <span role='status' className='text-muted-foreground text-xs'>
                {t('Loading...')}
              </span>
            )}
            {summary.isError && (
              <span role='status' className='text-destructive text-xs'>
                {t('Tool supply information is unavailable.')}
              </span>
            )}
          </div>
          <Button
            type='button'
            variant='ghost'
            size='xs'
            disabled={summary.isFetching}
            onClick={() => void summary.refetch()}
          >
            <RefreshCw
              className={cn('mr-1 size-3', summary.isFetching && 'animate-spin')}
              aria-hidden='true'
            />
            {t('Refresh')}
          </Button>
        </div>

        {items.length === 0 && !summary.isPending && !summary.isError && (
          <p className='text-muted-foreground text-sm'>{t('No tool providers')}</p>
        )}

        {items.length > 0 && (
          <>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='bg-muted/20 flex flex-col gap-2 rounded-lg border p-3'>
                <div className='flex items-center justify-between gap-2'>
                  <span className='flex items-center gap-1.5 text-sm font-medium'>
                    <Search className='text-muted-foreground size-4' aria-hidden='true' />
                    {t('Search')}
                  </span>
                  <Badge
                    variant={
                      searchChannels > 0 || globalExaReady ? 'default' : 'secondary'
                    }
                  >
                    {searchChannels > 0 || globalExaReady
                      ? t('Ready')
                      : t('Not configured')}
                  </Badge>
                </div>
                <div className='flex flex-wrap items-center gap-1.5 text-xs'>
                  <Badge variant='outline'>
                    {t('Channels: {{countText}}', {
                      countText: formatNumber(searchChannels, locale),
                    })}
                  </Badge>
                  {globalExaReady && (
                    <Badge variant='secondary'>{t('Global key configured')}</Badge>
                  )}
                  {missingMappings > 0 && (
                    <Badge variant='destructive'>
                      {t('Missing mappings: {{countText}}', {
                        countText: formatNumber(missingMappings, locale),
                      })}
                    </Badge>
                  )}
                </div>
                {activeSearchProviders.length > 0 && (
                  <p className='text-muted-foreground text-xs'>
                    {activeSearchProviders.map((item) => item.provider).join(' · ')}
                  </p>
                )}
              </div>

              <div className='bg-muted/20 flex flex-col gap-2 rounded-lg border p-3'>
                <div className='flex items-center justify-between gap-2'>
                  <span className='flex items-center gap-1.5 text-sm font-medium'>
                    <Globe className='text-muted-foreground size-4' aria-hidden='true' />
                    {t('Fetch')}
                  </span>
                  <Badge variant='default'>{t('Built-in local default')}</Badge>
                </div>
                <div className='flex flex-wrap items-center gap-1.5 text-xs'>
                  <Badge variant='secondary'>{t('Built-in fetch fallback')}</Badge>
                  {activeFetchProviders.map((item) => (
                    <Badge key={item.provider} variant='outline'>
                      {item.provider}
                    </Badge>
                  ))}
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Uses in-process local fetch by default without charging any channel quota.'
                  )}
                </p>
              </div>
            </div>

            <Collapsible open={detailsOpen} onOpenChange={setDetailsOpen}>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <CollapsibleTrigger
                  render={
                    <Button type='button' variant='ghost' size='xs' className='px-1.5' />
                  }
                >
                  <span>{t('Provider details')}</span>
                  <ChevronDown
                    className={cn(
                      'ml-1 size-3.5 transition-transform',
                      detailsOpen && 'rotate-180'
                    )}
                    aria-hidden='true'
                  />
                </CollapsibleTrigger>
                {detailsOpen && hiddenCount > 0 && (
                  <Button
                    type='button'
                    variant='ghost'
                    size='xs'
                    onClick={() => setShowUnconfigured((current) => !current)}
                  >
                    {showUnconfigured
                      ? t('Hide unconfigured')
                      : t('Show unconfigured ({{count}})', { count: hiddenCount })}
                  </Button>
                )}
              </div>
              <CollapsibleContent className='pt-2'>
                <div className='overflow-x-auto rounded-lg border'>
                  <Table withContainer={false}>
                    <TableHeader className='bg-muted/40'>
                      <TableRow>
                        <TableHead className='text-xs'>{t('Provider')}</TableHead>
                        <TableHead className='text-xs'>{t('Capabilities')}</TableHead>
                        <TableHead className='text-xs'>{t('Status')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {visibleRows.map((item) => {
                        const contracts = item.contracts ?? []
                        const nativeContracts = item.native_contracts ?? []
                        const hasSearch = contracts.some((c) =>
                          c.endsWith('/search')
                        )
                        const hasFetch = contracts.some((c) =>
                          c.endsWith('/fetch')
                        )
                        const nativeSearch = nativeContracts.some((c) =>
                          c.endsWith('/search')
                        )
                        const nativeFetch = nativeContracts.some((c) =>
                          c.endsWith('/fetch')
                        )
                        return (
                          <TableRow key={item.provider}>
                            <TableCell className='font-mono text-xs font-medium'>
                              {item.provider}
                            </TableCell>
                            <TableCell>
                              <div className='flex flex-wrap gap-1.5'>
                                {hasSearch && (
                                  <Badge variant='outline' className='text-[11px]'>
                                    {t('Search')}
                                    {nativeSearch && ` · ${t('Native')}`}
                                  </Badge>
                                )}
                                {hasFetch && (
                                  <Badge variant='outline' className='text-[11px]'>
                                    {t('Fetch')}
                                    {nativeFetch && ` · ${t('Native')}`}
                                  </Badge>
                                )}
                              </div>
                            </TableCell>
                            <TableCell>
                              <div className='flex flex-wrap items-center gap-1.5'>
                                {item.available_channels > 0 && (
                                  <Badge variant='outline' className='text-[11px]'>
                                    {t('Channels: {{countText}}', {
                                      countText: formatNumber(
                                        item.available_channels,
                                        locale
                                      ),
                                    })}
                                  </Badge>
                                )}
                                {item.global_credential_configured && (
                                  <Badge variant='secondary' className='text-[11px]'>
                                    {t('Global key configured')}
                                  </Badge>
                                )}
                                {item.local_fallback && (
                                  <Badge variant='secondary' className='text-[11px]'>
                                    {t('Built-in fetch fallback')}
                                  </Badge>
                                )}
                                {item.missing_mapping_channels > 0 && (
                                  <Badge variant='destructive' className='text-[11px]'>
                                    {t('Missing mappings: {{countText}}', {
                                      countText: formatNumber(
                                        item.missing_mapping_channels,
                                        locale
                                      ),
                                    })}
                                  </Badge>
                                )}
                                {!isConfiguredProvider(item) && (
                                  <span className='text-muted-foreground text-xs'>—</span>
                                )}
                              </div>
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                </div>
              </CollapsibleContent>
            </Collapsible>
          </>
        )}
      </CardContent>
    </Card>
  )
}
