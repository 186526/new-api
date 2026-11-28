import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  staticDataTableClassNames as tableClasses,
} from '@/components/data-table'
import { Empty, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import {
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { RequestObservationDistributionEntry } from '@/features/performance-metrics/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

type DistributionRow = {
  key: string
  label: string
  variant: string
  outcome: string
  count: string
  cacheHits?: string
}

export function RequestObservationDistribution(props: {
  data?: RequestObservationDistributionEntry[]
}) {
  const { t, i18n } = useTranslation()
  const role = useAuthStore((state) => state.auth.user?.role)
  if (!props.data || role == null || role < ROLE.ADMIN) return null

  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const unknown = t('Unknown')
  const toolOutcomes = {
    success: t('Success'),
    empty: t('No results'),
    upstream_error: t('Upstream error'),
    unavailable: t('Unavailable'),
  }
  const reasoningOutcomes = {
    applied: t('Policy applied'),
    passthrough: t('Request body passthrough'),
    unsupported_model: t('Unsupported model'),
    blacklisted: t('Model exempt from automatic reasoning'),
    model_modifier: t('Model suffix takes priority'),
    group_excluded: t('Group excluded by policy'),
    model_excluded: t('Model excluded by policy'),
    explicit_disable: t('Reasoning explicitly disabled'),
    explicit_strength: t('Explicit effort retained'),
    within_cap: t('Explicit effort within policy cap'),
  }
  const efforts = {
    none: t('None'),
    minimal: t('Minimal reasoning effort'),
    low: t('Low reasoning effort'),
    medium: t('Medium reasoning effort'),
    high: t('High reasoning effort'),
    xhigh: t('Extra high reasoning effort'),
  }
  const tools: DistributionRow[] = []
  const reasoning: DistributionRow[] = []
  for (const entry of props.data) {
    const key = JSON.stringify([entry.name, entry.variant, entry.outcome])
    const count = formatNumber(entry.count, locale)
    if (entry.kind === 'tool') {
      tools.push({
        key,
        count,
        label: displayIdentifier(entry.name, unknown),
        variant: displayIdentifier(entry.variant, unknown),
        outcome: knownLabel(toolOutcomes, entry.outcome, unknown),
        cacheHits: formatNumber(entry.cache_hits ?? 0, locale),
      })
    } else if (entry.kind === 'reasoning') {
      let tier = unknown
      if (entry.name === '0') tier = t('Overflow effort')
      else if (/^[1-6]$/.test(entry.name)) {
        tier = formatNumber(Number(entry.name), locale)
      }
      reasoning.push({
        key,
        count,
        label: tier,
        variant: knownLabel(efforts, entry.variant, unknown),
        outcome: knownLabel(reasoningOutcomes, entry.outcome, unknown),
      })
    }
  }

  if (!tools.length && !reasoning.length) {
    return (
      <Empty className='p-3'>
        <EmptyHeader>
          <EmptyTitle>{t('No request distribution samples')}</EmptyTitle>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className='flex min-w-0 flex-col gap-3'>
      {tools.length > 0 && (
        <DistributionTable
          title={t('Tool request distribution')}
          labelHeading={t('Tool API')}
          variantHeading={t('Provider')}
          cacheHeading={t('Tool cache hits')}
          rows={tools}
        />
      )}
      {reasoning.length > 0 && (
        <DistributionTable
          title={t('Automatic reasoning distribution')}
          labelHeading={t('Matched tier')}
          variantHeading={t('Reasoning effort')}
          rows={reasoning}
        />
      )}
    </div>
  )
}

function DistributionTable(props: {
  title: string
  labelHeading: string
  variantHeading: string
  cacheHeading?: string
  rows: DistributionRow[]
}) {
  const { t } = useTranslation()
  const titleId = useId()
  return (
    <section className='flex min-w-0 flex-col gap-1'>
      <h4 id={titleId} className='text-sm font-medium'>
        {props.title}
      </h4>
      <StaticDataTable
        className='max-h-64 max-w-full overflow-auto focus-visible:outline focus-visible:outline-offset-2'
        containerProps={{
          role: 'region',
          tabIndex: 0,
          'aria-labelledby': titleId,
        }}
        tableProps={{ withContainer: false }}
      >
        <TableCaption className='sr-only'>{props.title}</TableCaption>
        <TableHeader className='bg-card sticky top-0'>
          <TableRow>
            <TableHead scope='col' className={tableClasses.compactHeaderCell}>
              {props.labelHeading}
            </TableHead>
            <TableHead scope='col' className={tableClasses.compactHeaderCell}>
              {props.variantHeading}
            </TableHead>
            <TableHead scope='col' className={tableClasses.compactHeaderCell}>
              {t('Result')}
            </TableHead>
            <TableHead
              scope='col'
              className={tableClasses.compactHeaderCellRight}
            >
              {t('Requests')}
            </TableHead>
            {props.cacheHeading && (
              <TableHead
                scope='col'
                className={tableClasses.compactHeaderCellRight}
              >
                {props.cacheHeading}
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody className='[&>tr]:h-auto'>
          {props.rows.map((row) => (
            <TableRow key={row.key}>
              <TableHead scope='row' className='h-auto py-2'>
                {row.label}
              </TableHead>
              <TableCell>{row.variant}</TableCell>
              <TableCell>{row.outcome}</TableCell>
              <TableCell className={tableClasses.compactNumericCell}>
                {row.count}
              </TableCell>
              {props.cacheHeading && (
                <TableCell className={tableClasses.compactNumericCell}>
                  {row.cacheHits}
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </StaticDataTable>
    </section>
  )
}

function displayIdentifier(value: string, fallback: string): string {
  return value && value !== 'unknown' ? value : fallback
}

function knownLabel(
  labels: Record<string, string>,
  value: string,
  fallback: string
): string {
  return Object.hasOwn(labels, value) ? labels[value] : fallback
}
