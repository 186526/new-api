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
  normalizeProviderOrder,
  TOOL_CONTRACTS,
  TOOL_ENDPOINTS,
  TOOL_POLICY_LABELS,
  type GroupPolicies,
} from '../lib/group-policies'

type GroupPolicyMatrixProps = {
  groups: string[]
  policies: GroupPolicies
  selectedGroup: string
  onGroupChange: (group: string) => void
  disabled: boolean
}

export function GroupPolicyMatrix(props: GroupPolicyMatrixProps) {
  const { t } = useTranslation()
  const titleId = useId()
  return (
    <div
      role='region'
      aria-labelledby={titleId}
      tabIndex={0}
      className='relative max-h-96 max-w-full overflow-auto rounded-md border'
    >
      <Table withContainer={false}>
        <TableCaption id={titleId} className='sr-only'>
          {t('Group policy overview')}
        </TableCaption>
        <TableHeader className='bg-background sticky top-0 z-20'>
          <TableRow>
            <TableHead
              scope='col'
              className='bg-background sticky start-0 z-10'
            >
              {t('Group')}
            </TableHead>
            {TOOL_CONTRACTS.map((contract) => (
              <TableHead key={contract} scope='col'>
                <code dir='ltr'>{TOOL_ENDPOINTS[contract]}</code>
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.groups.map((group) => (
            <TableRow
              key={group}
              data-state={
                props.selectedGroup === group ? 'selected' : undefined
              }
            >
              <TableHead
                scope='row'
                className='bg-background sticky start-0 z-10'
              >
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  disabled={props.disabled}
                  aria-pressed={props.selectedGroup === group}
                  onClick={() => props.onGroupChange(group)}
                >
                  {group}
                </Button>
              </TableHead>
              {TOOL_CONTRACTS.map((contract) => {
                const rule = Object.hasOwn(props.policies, group)
                  ? props.policies[group][contract]
                  : undefined
                return (
                  <TableCell key={contract}>
                    <Badge variant='outline'>
                      {t(TOOL_POLICY_LABELS[rule?.mode ?? 'inherit'])}
                    </Badge>
                    {!!rule?.providers?.length && (
                      <code dir='ltr' className='ms-2'>
                        {normalizeProviderOrder(rule.providers, contract).join(' → ')}
                      </code>
                    )}
                  </TableCell>
                )
              })}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
