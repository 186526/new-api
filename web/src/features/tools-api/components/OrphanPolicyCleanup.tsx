import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { getGroups } from '@/features/users/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  getPolicySnapshot,
  policiesKey,
  readPolicyConflict,
  savePolicySnapshot,
} from '../lib/admin'
import { readGroupPolicies, type GroupPolicies } from '../lib/group-policies'

export function OrphanPolicyCleanup(props: {
  policies: GroupPolicies
  groups: string[]
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [targets, setTargets] = useState<string[]>([])
  const orphans = Object.keys(props.policies).filter(
    (group) => group !== 'auto' && !props.groups.includes(group)
  )
  const cleanup = useMutation({
    mutationFn: async (names: string[]) => {
      const snapshot = await getPolicySnapshot()
      const response = requireServerSuccess(await getGroups())
      if (!Array.isArray(response.data)) {
        throw new Error(t('toolsApi.loadFailed'))
      }
      const policies = readGroupPolicies(snapshot.value)
      if (!policies) throw new Error(t('toolsApi.invalidRules'))
      const groups = new Set(response.data)
      if (
        names.some(
          (name) =>
            groups.has(name) ||
            !Object.hasOwn(policies, name) ||
            JSON.stringify(policies[name]) !==
              JSON.stringify(props.policies[name])
        )
      ) {
        client.setQueryData(policiesKey, snapshot)
        void client.invalidateQueries({ queryKey: ['groups'] })
        throw new Error(
          t('The group list or policies changed. Review and retry.')
        )
      }
      for (const name of names) delete policies[name]
      return savePolicySnapshot({
        version: snapshot.version,
        value: JSON.stringify(policies),
      })
    },
    onSuccess: (snapshot) => {
      client.setQueryData(policiesKey, snapshot)
      setTargets([])
      toast.success(t('Orphan policies removed'))
    },
    onError: (error) => {
      const conflict = readPolicyConflict(error)
      if (conflict) client.setQueryData(policiesKey, conflict)
      setTargets([])
      handleServerError(error, t('Could not remove orphan policies'))
    },
  })

  if (!orphans.length && !targets.length) return null
  return (
    <div className='flex justify-end'>
      <Button
        type='button'
        variant='outline'
        onClick={() => setTargets(orphans)}
        disabled={!orphans.length || cleanup.isPending}
      >
        {t('Remove orphan policies ({{count}})', { count: orphans.length })}
      </Button>
      <ConfirmDialog
        open={targets.length > 0}
        onOpenChange={(open) => {
          if (!open && !cleanup.isPending) setTargets([])
        }}
        title={t('Remove orphan policies?')}
        desc={
          <>
            <p>{t('Remove tool rules for these deleted groups:')}</p>
            <p className='mt-2 wrap-anywhere'>{targets.join(', ')}</p>
          </>
        }
        confirmText={t('Remove policies')}
        handleConfirm={() => cleanup.mutate(targets)}
        destructive
        isLoading={cleanup.isPending}
      />
    </div>
  )
}
