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
  getMistralUsage,
  type MistralUsageBudget,
  type MistralUsageData,
} from '../../api'

type MistralUsageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  channelId: number
  channelName: string
}

function clampRatio(value: number | undefined): number {
  if (value === undefined || Number.isNaN(value)) return 0
  return Math.min(1, Math.max(0, value))
}

function hasAmount(value: number | undefined): value is number {
  return value !== undefined && Number.isFinite(value) && value > 0
}

function formatAmount(value: number): string {
  return formatCurrencyFromUSD(value, {
    digitsLarge: 2,
    digitsSmall: 4,
    showSymbol: true,
  })
}

function budgetLabelKey(scope: MistralUsageBudget['scope']): string {
  return scope === 'api' ? 'API usage' : 'Vibe Code usage'
}

function UsageRow(props: { label: string; value: string }) {
  return (
    <div className='flex items-baseline justify-between gap-2 text-sm'>
      <span className='text-muted-foreground'>{props.label}</span>
      <span className='font-medium'>{props.value}</span>
    </div>
  )
}

function BudgetSection(props: {
  budget: MistralUsageBudget
  percent: Intl.NumberFormat
}) {
  const { t } = useTranslation()
  const remaining = clampRatio(props.budget.remaining_ratio)
  const remainingLabel = props.percent.format(remaining)
  const label = t(budgetLabelKey(props.budget.scope))
  const remainingAmount = hasAmount(props.budget.remaining_usd)
    ? formatAmount(props.budget.remaining_usd)
    : null
  const limitAmount = hasAmount(props.budget.limit_usd)
    ? formatAmount(props.budget.limit_usd)
    : null
  const amountLabel =
    remainingAmount && limitAmount
      ? `${remainingAmount} / ${limitAmount}`
      : remainingAmount

  return (
    <div className='space-y-2'>
      <div className='flex items-baseline justify-between gap-2'>
        <span className='text-sm font-medium'>{label}</span>
        <span className='text-sm font-medium'>{remainingLabel}</span>
      </div>
      <Progress
        value={remaining * 100}
        aria-label={`${label}: ${remainingLabel}`}
        className='mt-1'
      />
      {amountLabel && (
        <UsageRow label={t('Remaining amount')} value={amountLabel} />
      )}
      {props.budget.reset_at ? (
        <UsageRow
          label={t('Reset at:')}
          value={formatTimestampToDate(props.budget.reset_at)}
        />
      ) : null}
    </div>
  )
}

function MistralUsageDetails(props: {
  usage: MistralUsageData
  locale: string | undefined
}) {
  const { t } = useTranslation()
  const percent = new Intl.NumberFormat(props.locale, {
    style: 'percent',
    maximumFractionDigits: 1,
  })

  return (
    <div className='space-y-5'>
      {props.usage.budgets.length === 0 ? (
        <div className='text-muted-foreground text-sm'>
          {t('No usage data')}
        </div>
      ) : (
        props.usage.budgets.map((budget) => (
          <BudgetSection
            key={budget.scope}
            budget={budget}
            percent={percent}
          />
        ))
      )}
      <div className='space-y-2'>
        <UsageRow
          label={t('Pay-as-you-go')}
          value={props.usage.payg_enabled ? t('Enabled') : t('Disabled')}
        />
        {props.usage.fetched_at ? (
          <UsageRow
            label={t('Last updated')}
            value={formatTimestampToDate(props.usage.fetched_at)}
          />
        ) : null}
      </div>
    </div>
  )
}

export function MistralUsageDialog(props: MistralUsageDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['channels', props.channelId, 'mistral-usage'],
    queryFn: async ({ signal }) => {
      const response = await getMistralUsage(props.channelId, signal)
      signal.throwIfAborted()
      if (!response.success) {
        throw new Error(response.message || t('Failed to fetch usage'))
      }
      return response.data
    },
    enabled: props.open,
    refetchInterval: false,
    refetchOnWindowFocus: false,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Mistral usage')}
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
      {!query.isLoading && !query.isError && !query.data && (
        <div className='text-muted-foreground text-sm'>
          {t('No usage data')}
        </div>
      )}
      {!query.isLoading && !query.isError && query.data && (
        <MistralUsageDetails usage={query.data} locale={locale} />
      )}
    </Dialog>
  )
}
