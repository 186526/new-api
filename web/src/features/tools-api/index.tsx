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
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { getGroups } from '@/features/users/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { GroupPolicyEditor } from './components/GroupPolicyEditor'
import { OrphanPolicyCleanup } from './components/OrphanPolicyCleanup'
import { getPolicySnapshot, policiesKey } from './lib/admin'
import { readGroupPolicies } from './lib/group-policies'

export function ToolsApiContent() {
  const { t } = useTranslation()
  const options = useQuery({
    queryKey: policiesKey,
    queryFn: getPolicySnapshot,
  })
  const groupList = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
  })

  const policies = readGroupPolicies(options.data?.value)

  if (options.isLoading || groupList.isLoading) return <LoadingState />
  if (!options.data || !groupList.data || !policies) {
    return (
      <ErrorState
        title={t('toolsApi.loadFailed')}
        description={policies === null ? t('toolsApi.invalidRules') : undefined}
        onRetry={() => {
          void options.refetch()
          void groupList.refetch()
        }}
      />
    )
  }

  const groups = [...new Set(groupList.data.data ?? [])].sort()
  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <OrphanPolicyCleanup policies={policies} groups={groups} />
      {groups.length === 0 ? (
        <EmptyState
          title={t('toolsApi.noGroups')}
          description={t('toolsApi.createGroup')}
        />
      ) : (
        <GroupPolicyEditor policies={policies} groups={groups} />
      )}
    </div>
  )
}
