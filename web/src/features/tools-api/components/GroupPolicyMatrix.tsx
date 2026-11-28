import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import {
  FETCH_CONTRACTS,
  SEARCH_CONTRACTS,
  TOOL_POLICY_LABELS,
  type GroupPolicies,
  type ToolContract,
} from '../lib/group-policies'

type GroupPolicyMatrixProps = {
  groups: string[]
  policies: GroupPolicies
  selectedGroup: string
  onGroupChange: (group: string) => void
  disabled: boolean
}

const MODE_BADGE_VARIANT = {
  inherit: 'outline',
  prefer: 'secondary',
  only: 'default',
  disabled: 'destructive',
} as const

function summarizeCapability(
  policies: GroupPolicies,
  group: string,
  contracts: ToolContract[]
) {
  const groupRules = Object.hasOwn(policies, group) ? policies[group] : {}
  const normalized = contracts.map((contract) => {
    const rule = groupRules[contract]
    return {
      mode: rule?.mode ?? ('inherit' as const),
      providers: rule?.providers ?? [],
    }
  })
  const first = normalized[0]
  const mixed = normalized.some(
    (entry) =>
      entry.mode !== first.mode ||
      entry.providers.join(',') !== first.providers.join(',')
  )
  return { ...first, mixed }
}

export function GroupPolicyMatrix(props: GroupPolicyMatrixProps) {
  const { t } = useTranslation()
  const titleId = useId()
  return (
    <div
      role='region'
      aria-labelledby={titleId}
      tabIndex={0}
      className='relative max-h-72 max-w-full overflow-auto rounded-lg border'
    >
      <Table withContainer={false}>
        <TableCaption id={titleId} className='sr-only'>
          {t('Group policy overview')}
        </TableCaption>
        <TableHeader className='bg-muted/40 sticky top-0 z-20'>
          <TableRow>
            <TableHead scope='col' className='bg-muted/40 sticky start-0 z-10'>
              {t('Group')}
            </TableHead>
            <TableHead scope='col' className='text-xs'>
              {t('Search')}
            </TableHead>
            <TableHead scope='col' className='text-xs'>
              {t('Fetch')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.groups.map((group) => {
            const isSelected = props.selectedGroup === group
            const searchSummary = summarizeCapability(
              props.policies,
              group,
              SEARCH_CONTRACTS
            )
            const fetchSummary = summarizeCapability(
              props.policies,
              group,
              FETCH_CONTRACTS
            )
            return (
              <TableRow
                key={group}
                data-state={isSelected ? 'selected' : undefined}
                className={isSelected ? 'bg-primary/5' : undefined}
              >
                <TableHead
                  scope='row'
                  className='bg-background sticky start-0 z-10'
                >
                  <Button
                    type='button'
                    variant={isSelected ? 'secondary' : 'ghost'}
                    size='sm'
                    disabled={props.disabled}
                    aria-pressed={isSelected}
                    onClick={() => props.onGroupChange(group)}
                    className='font-medium'
                  >
                    {group}
                  </Button>
                </TableHead>
                {[
                  { key: 'search', summary: searchSummary },
                  { key: 'fetch', summary: fetchSummary },
                ].map(({ key, summary }) => (
                  <TableCell
                    key={key}
                    onClick={() => {
                      if (!props.disabled) props.onGroupChange(group)
                    }}
                    className={!props.disabled ? 'cursor-pointer' : undefined}
                  >
                    {!summary.mixed && summary.mode === 'inherit' ? (
                      <span
                        className='text-muted-foreground text-xs'
                        aria-label={t(TOOL_POLICY_LABELS.inherit)}
                      >
                        —
                      </span>
                    ) : (
                      <div className='flex flex-wrap items-center gap-1.5'>
                        {summary.mixed ? (
                          <Badge variant='outline'>
                            {t('Per-protocol overrides')}
                          </Badge>
                        ) : (
                          <>
                            <Badge variant={MODE_BADGE_VARIANT[summary.mode]}>
                              {t(TOOL_POLICY_LABELS[summary.mode])}
                            </Badge>
                            {summary.providers.length > 0 && (
                              <code
                                dir='ltr'
                                className='text-muted-foreground text-xs'
                              >
                                {summary.providers.join(' → ')}
                              </code>
                            )}
                          </>
                        )}
                      </div>
                    )}
                  </TableCell>
                ))}
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}
