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
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestampToDate } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  getAntigravityUsage,
  type AntigravityUsageBucket,
  type AntigravityUsageGroup,
} from '../../api'

type AntigravityUsageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  channelId: number
  channelName: string
}

function resetTimestamp(value?: string): number | undefined {
  if (!value) return undefined
  const parsed = Date.parse(value)
  if (Number.isNaN(parsed)) return undefined
  return Math.floor(parsed / 1000)
}

function bucketLabel(bucket: AntigravityUsageBucket): string {
  return (
    bucket.display_name?.trim() ||
    bucket.model_id?.trim() ||
    bucket.bucket_id?.trim() ||
    '-'
  )
}

export function AntigravityUsageDialog(props: AntigravityUsageDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const percent = new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 1,
  })
  const query = useQuery({
    queryKey: ['channels', props.channelId, 'antigravity-usage'],
    queryFn: async ({ signal }) => {
      const response = await getAntigravityUsage(props.channelId, signal)
      signal.throwIfAborted()
      if (!response.success) {
        throw new Error(response.message || t('Failed to fetch usage'))
      }
      return response.data?.groups ?? []
    },
    enabled: props.open,
    refetchInterval: false,
    refetchOnWindowFocus: false,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })

  const groups: AntigravityUsageGroup[] = query.data ?? []

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Antigravity usage')}
      description={props.channelName}
      contentClassName='sm:max-w-2xl'
      bodyClassName='space-y-5'
      footer={
        <Button variant='outline' onClick={() => props.onOpenChange(false)}>
          {t('Close')}
        </Button>
      }
    >
      <div className='flex justify-end'>
        <Button
          variant='outline'
          size='sm'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RefreshCw
            className={
              query.isFetching ? 'mr-2 h-4 w-4 animate-spin' : 'mr-2 h-4 w-4'
            }
          />
          {t('Refresh')}
        </Button>
      </div>

      {query.isLoading && <LoadingState />}
      {query.isError && (
        <ErrorState
          description={getServerErrorMessage(
            query.error,
            t('Failed to fetch usage')
          )}
          onRetry={() => void query.refetch()}
        />
      )}
      {!query.isLoading && !query.isError && groups.length === 0 && (
        <div className='text-muted-foreground text-sm'>
          {t('No usage data')}
        </div>
      )}
      {!query.isLoading &&
        !query.isError &&
        groups.map((group, groupIndex) => (
          <section key={group.display_name ?? groupIndex} className='space-y-3'>
            <div>
              <h3 className='text-sm font-medium'>
                {group.display_name || t('Models')}
              </h3>
              {group.description && (
                <p className='text-muted-foreground mt-1 text-xs'>
                  {group.description}
                </p>
              )}
            </div>
            <div className='space-y-4'>
              {group.buckets.map((bucket, bucketIndex) => {
                const remaining = Math.min(
                  1,
                  Math.max(0, bucket.remaining_fraction)
                )
                const resetTime = resetTimestamp(bucket.reset_time)
                return (
                  <div key={bucket.bucket_id ?? bucket.model_id ?? bucketIndex}>
                    <div className='flex items-baseline justify-between gap-2'>
                      <span className='text-sm'>{bucketLabel(bucket)}</span>
                      <span className='text-sm font-medium'>
                        {percent.format(remaining)}
                      </span>
                    </div>
                    <Progress
                      value={remaining * 100}
                      aria-label={`${bucketLabel(bucket)}: ${percent.format(remaining)}`}
                      className='mt-1'
                    />
                    <div className='text-muted-foreground mt-1 flex flex-wrap justify-between gap-2 text-xs'>
                      <span>{bucket.window ?? ''}</span>
                      {resetTime !== undefined && (
                        <span>
                          {t('Reset at:')} {formatTimestampToDate(resetTime)}
                        </span>
                      )}
                    </div>
                    {bucket.description && (
                      <p className='text-muted-foreground mt-1 text-xs'>
                        {bucket.description}
                      </p>
                    )}
                  </div>
                )
              })}
            </div>
          </section>
        ))}
    </Dialog>
  )
}
