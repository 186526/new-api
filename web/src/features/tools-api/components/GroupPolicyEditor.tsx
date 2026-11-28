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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Spinner } from '@/components/ui/spinner'
import { FormNavigationGuard } from '@/features/system-settings/components/form-navigation-guard'
import { useResetForm } from '@/features/system-settings/hooks/use-reset-form'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getPolicySnapshot,
  getProviderSummary,
  policiesKey,
  readPolicyConflict,
  savePolicySnapshot,
  summaryKey,
} from '../lib/admin'
import {
  createGroupPolicyFormSchema,
  readGroupPolicies,
  groupPolicyFormValues,
  TOOL_CONTRACTS,
  TOOL_ENDPOINTS,
  TOOL_POLICY_LABELS,
  TOOL_PROVIDERS,
  updateGroupPolicies,
  type GroupPolicies,
  type GroupPolicyFormValues,
} from '../lib/group-policies'
import { GroupPolicyMatrix } from './GroupPolicyMatrix'
import { RouteSimulator } from './RouteSimulator'
import { ToolEndpointRule } from './ToolEndpointRule'

type GroupPolicyEditorProps = { policies: GroupPolicies; groups: string[] }

export function GroupPolicyEditor(props: GroupPolicyEditorProps) {
  const [selectedGroup, setSelectedGroup] = useState<string>()
  const group =
    selectedGroup !== undefined && props.groups.includes(selectedGroup)
      ? selectedGroup
      : props.groups[0]

  return (
    <GroupPolicyForm
      group={group}
      groups={props.groups}
      policies={props.policies}
      onGroupChange={setSelectedGroup}
    />
  )
}

type GroupPolicyFormProps = GroupPolicyEditorProps & {
  group: string
  onGroupChange: (group: string) => void
}

function GroupPolicyForm(props: GroupPolicyFormProps) {
  const { t } = useTranslation()
  const groupId = useId()
  const titleId = useId()
  const providerHelpId = useId()
  const copyGroupId = useId()
  const queryClient = useQueryClient()
  const [conflictRules, setConflictRules] = useState<GroupPolicyFormValues>()
  const directoryQuery = useQuery({
    queryKey: summaryKey,
    queryFn: getProviderSummary,
    meta: { errorToast: false },
  })
  const directory = directoryQuery.isError ? undefined : directoryQuery.data
  const schema = useMemo(
    () => createGroupPolicyFormSchema(t, directory),
    [t, directory]
  )
  const defaults = useMemo(
    () => groupPolicyFormValues(props.policies, props.group),
    [props.policies, props.group]
  )
  const form = useForm<GroupPolicyFormValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  const { isDirty, dirtyFields, isSubmitting } = form.formState
  useResetForm(form, isDirty || isSubmitting ? undefined : defaults)

  function applyRules(values: GroupPolicyFormValues) {
    for (const contract of TOOL_CONTRACTS) {
      form.setValue(`rules.${contract}.mode`, values.rules[contract].mode, {
        shouldDirty: true,
      })
      form.setValue(`rules.${contract}.order`, values.rules[contract].order, {
        shouldDirty: true,
      })
    }
  }

  function keepDraft() {
    if (!conflictRules) return
    const draft = form.getValues()
    const edited = TOOL_CONTRACTS.filter((contract) => dirtyFields.rules?.[contract])
    form.reset(conflictRules)
    for (const contract of edited) {
      form.setValue(`rules.${contract}.mode`, draft.rules[contract].mode, {
        shouldDirty: true,
      })
      form.setValue(`rules.${contract}.order`, draft.rules[contract].order, {
        shouldDirty: true,
      })
    }
    setConflictRules(undefined)
  }

  async function save(values: GroupPolicyFormValues) {
    if (!isDirty || conflictRules) return

    try {
      const snapshot = await getPolicySnapshot()
      const policies = readGroupPolicies(snapshot.value)
      if (policies === null) throw new Error(t('toolsApi.invalidRules'))
      queryClient.setQueryData(policiesKey, snapshot)
      const latest = groupPolicyFormValues(policies, props.group)
      const originalRules = form.formState.defaultValues?.rules
      const hasConflict = TOOL_CONTRACTS.some((contract) => {
        if (!dirtyFields.rules?.[contract]) return false
        const originalRule = originalRules?.[contract]
        const latestRule = latest.rules[contract]
        return latestRule.mode !== originalRule?.mode || latestRule.order !== originalRule?.order
      })
      if (hasConflict) {
        setConflictRules(latest)
        return
      }
      const next = updateGroupPolicies(policies, props.group, values, dirtyFields.rules ?? {})
      const saved = await savePolicySnapshot({
        value: JSON.stringify(next),
        version: snapshot.version,
      })
      queryClient.setQueryData(policiesKey, saved)
      form.reset(groupPolicyFormValues(readGroupPolicies(saved.value) ?? next, props.group))
    } catch (error) {
      const conflict = readPolicyConflict(error)
      const latest = conflict && readGroupPolicies(conflict.value)
      if (conflict && latest) {
        queryClient.setQueryData(policiesKey, conflict)
        setConflictRules(groupPolicyFormValues(latest, props.group))
      } else {
        handleServerError(error, t('toolsApi.loadFailed'))
      }
    }
  }

  return (
    <div className='mx-auto flex w-full max-w-4xl min-w-0 flex-col gap-4 pb-3'>
      <FormNavigationGuard when={isDirty} />
      <div className='flex justify-end'>
        <RouteSimulator groups={props.groups} selectedGroup={props.group} />
      </div>
      <form aria-labelledby={titleId} onSubmit={form.handleSubmit(save)}>
        <Card className='@container'>
          <CardHeader>
            <CardTitle>
              <h3 id={titleId}>{t('toolsApi.rulesByGroup')}</h3>
            </CardTitle>
          </CardHeader>
          <CardContent className='flex min-w-0 flex-col gap-4'>
            <GroupPolicyMatrix
              groups={props.groups}
              policies={props.policies}
              selectedGroup={props.group}
              onGroupChange={props.onGroupChange}
              disabled={isDirty || isSubmitting}
            />
            <FieldGroup>
              <Field
                data-disabled={isDirty || isSubmitting}
                className='max-w-xs'
              >
                <FieldLabel htmlFor={groupId}>{t('Group')}</FieldLabel>
                <NativeSelect
                  id={groupId}
                  name='group'
                  value={props.group}
                  disabled={isDirty || isSubmitting}
                  onChange={(event) => props.onGroupChange(event.target.value)}
                  className='w-full pointer-coarse:[&>select]:min-h-12'
                >
                  {props.groups.map((group) => (
                    <NativeSelectOption key={group} value={group}>
                      {group}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                {isDirty && (
                  <FieldDescription>
                    {t('toolsApi.switchGroupHelp')}
                  </FieldDescription>
                )}
              </Field>
              <Field className='max-w-xs'>
                <FieldLabel htmlFor={copyGroupId}>
                  {t('Copy rules from group')}
                </FieldLabel>
                <NativeSelect
                  id={copyGroupId}
                  value=''
                  disabled={isSubmitting || props.groups.length < 2}
                  onChange={(event) =>
                    applyRules(
                      groupPolicyFormValues(props.policies, event.target.value)
                    )
                  }
                >
                  <NativeSelectOption value='' disabled>
                    {t('Select a group')}
                  </NativeSelectOption>
                  {props.groups
                    .filter((group) => group !== props.group)
                    .map((group) => (
                      <NativeSelectOption key={group} value={group}>
                        {group}
                      </NativeSelectOption>
                    ))}
                </NativeSelect>
              </Field>
              <Button
                type='button'
                variant='outline'
                className='self-start'
                disabled={isSubmitting}
                onClick={() =>
                  applyRules(groupPolicyFormValues({}, props.group))
                }
              >
                {t('Reset to inherited rules')}
              </Button>
            </FieldGroup>
            <div
              id={providerHelpId}
              className='text-muted-foreground flex max-w-2xl flex-col gap-1 text-sm'
            >
              <p>{t('toolsApi.orderHelp')}</p>
              <p>
                {t('toolsApi.supportedProviders')}{' '}
                <code dir='ltr'>{(directory?.map((entry) => entry.provider) ?? TOOL_PROVIDERS).join(', ')}</code>
              </p>
            </div>
            {directoryQuery.isError && (
              <Alert variant='destructive'>
                <AlertDescription>{t('Provider directory is unavailable. Existing selections are preserved.')}</AlertDescription>
                <Button type='button' variant='outline' size='sm' disabled={directoryQuery.isFetching} onClick={() => void directoryQuery.refetch()}>
                  {t('Retry')}
                </Button>
              </Alert>
            )}
            {conflictRules && (
              <Alert>
                <AlertTitle>{t('Group rules changed. Review your draft before retrying.')}</AlertTitle>
                <AlertDescription>
                  <p>{t('Current server rules')}</p>
                  <ul className='list-disc ps-5'>
                    {TOOL_CONTRACTS.filter((contract) => dirtyFields.rules?.[contract]).map((contract) => {
                      const rule = conflictRules.rules[contract]
                      return <li key={contract}>{TOOL_ENDPOINTS[contract]}: {t(TOOL_POLICY_LABELS[rule.mode])}{rule.order && ` · ${rule.order}`}</li>
                    })}
                  </ul>
                </AlertDescription>
                <Button type='button' variant='outline' size='sm' onClick={keepDraft}>
                  {t('Keep draft')}
                </Button>
              </Alert>
            )}
            <div className='divide-y border-y'>
              {TOOL_CONTRACTS.map((contract) => (
                <ToolEndpointRule
                  key={contract}
                  contract={contract}
                  control={form.control}
                  disabled={isSubmitting}
                  providerHelpId={providerHelpId}
                  directory={directory}
                />
              ))}
            </div>
          </CardContent>
          <CardFooter className='flex flex-wrap justify-end gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={(!isDirty && !conflictRules) || isSubmitting}
              onClick={() => {
                form.reset(conflictRules ?? defaults)
                setConflictRules(undefined)
              }}
              className='pointer-coarse:min-h-12'
            >
              {t('Discard changes')}
            </Button>
            <Button
              type='submit'
              disabled={!isDirty || isSubmitting || !!conflictRules}
              className='pointer-coarse:min-h-12'
            >
              {isSubmitting && (
                <Spinner data-icon='inline-start' aria-hidden='true' />
              )}
              {isSubmitting ? t('Saving...') : t('Save changes')}
            </Button>
          </CardFooter>
        </Card>
      </form>
    </div>
  )
}
