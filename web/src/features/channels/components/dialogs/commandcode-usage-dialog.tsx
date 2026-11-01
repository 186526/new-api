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
import { formatCurrencyFromUSD } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  getCommandCodeUsage,
  type CommandCodeUsageAccount,
  type CommandCodeUsageWindow,
} from '../../api'

type CommandCodeUsageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  channelId: number
  channelName: string
}

function formatAmount(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return '-'
  return formatCurrencyFromUSD(value, {
    digitsLarge: 2,
    digitsSmall: 4,
    showSymbol: true,
  })
}

function remainingRatio(window: CommandCodeUsageWindow): number {
  if (!Number.isFinite(window.cap_usd) || window.cap_usd <= 0) return 0
  const ratio = 1 - window.used_usd / window.cap_usd
  return Math.min(1, Math.max(0, ratio))
}

function UsageRow(props: { label: string; value: string }) {
  return (
    <div className='flex items-baseline justify-between gap-2 text-sm'>
      <span className='text-muted-foreground'>{props.label}</span>
      <span className='font-medium'>{props.value}</span>
    </div>
  )
}

function WindowUsage(props: {
  window: CommandCodeUsageWindow
  locale: string | undefined
}) {
  const { t } = useTranslation()
  const percent = new Intl.NumberFormat(props.locale, {
    style: 'percent',
    maximumFractionDigits: 1,
  })
  const remaining = remainingRatio(props.window)
  const remainingLabel = percent.format(remaining)

  return (
    <div>
      <div className='flex items-baseline justify-between gap-2'>
        <span className='text-sm'>
          {props.window.name} · {formatAmount(props.window.used_usd)} /{' '}
          {formatAmount(props.window.cap_usd)}
        </span>
        <span className='text-sm font-medium'>{remainingLabel}</span>
      </div>
      <Progress
        value={remaining * 100}
        aria-label={`${props.window.name}: ${remainingLabel}`}
        className='mt-1'
      />
      {props.window.reset_at ? (
        <div className='text-muted-foreground mt-1 text-right text-xs'>
          {t('Reset at:')} {formatTimestampToDate(props.window.reset_at)}
        </div>
      ) : null}
    </div>
  )
}

function AccountUsage(props: {
  account: CommandCodeUsageAccount
  index: number
  locale: string | undefined
}) {
  const { t } = useTranslation()
  const windows = props.account.windows ?? []

  return (
    <section className='space-y-3'>
      <h3 className='text-sm font-medium'>
        {t('Account {{index}}', { index: props.index + 1 })}
      </h3>
      <div className='space-y-2'>
        <UsageRow
          label={t('Remaining amount')}
          value={formatAmount(props.account.remaining_usd)}
        />
        {props.account.monthly_credits !== undefined && (
          <UsageRow
            label={t('Monthly credits')}
            value={formatAmount(props.account.monthly_credits)}
          />
        )}
        {props.account.purchased_credits !== undefined && (
          <UsageRow
            label={t('Purchased credits')}
            value={formatAmount(props.account.purchased_credits)}
          />
        )}
        {props.account.free_credits !== undefined && (
          <UsageRow
            label={t('Free credits')}
            value={formatAmount(props.account.free_credits)}
          />
        )}
      </div>
      <div className='space-y-4'>
        {windows.map((window) => (
          <WindowUsage
            key={window.name}
            window={window}
            locale={props.locale}
          />
        ))}
      </div>
    </section>
  )
}

export function CommandCodeUsageDialog(props: CommandCodeUsageDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['channels', props.channelId, 'commandcode-usage'],
    queryFn: async ({ signal }) => {
      const response = await getCommandCodeUsage(props.channelId, signal)
      signal.throwIfAborted()
      if (!response.success) {
        throw new Error(response.message || t('Failed to fetch usage'))
      }
      return response.data?.accounts ?? []
    },
    enabled: props.open,
    refetchInterval: false,
    refetchOnWindowFocus: false,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })

  const accounts: CommandCodeUsageAccount[] = query.data ?? []

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Command Code usage')}
      description={props.channelName}
      contentClassName='sm:max-w-lg'
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
      {!query.isLoading && !query.isError && accounts.length === 0 && (
        <div className='text-muted-foreground text-sm'>
          {t('No usage data')}
        </div>
      )}
      {!query.isLoading &&
        !query.isError &&
        accounts.map((account, index) => (
          <AccountUsage
            key={
              account.remaining_usd ??
              account.monthly_credits ??
              account.purchased_credits ??
              account.free_credits ??
              ''
            }
            account={account}
            index={index}
            locale={locale}
          />
        ))}
    </Dialog>
  )
}
