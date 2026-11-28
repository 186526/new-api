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
import { ChevronDown, Globe, Search } from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Spinner } from '@/components/ui/spinner'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { FormNavigationGuard } from '@/features/system-settings/components/form-navigation-guard'
import { useResetForm } from '@/features/system-settings/hooks/use-reset-form'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  getPolicySnapshot,
  getProviderSummary,
  policiesKey,
  readPolicyConflict,
  savePolicySnapshot,
  summaryKey,
  type ProviderSummary,
} from '../lib/admin'
import {
  createGroupPolicyFormSchema,
  FETCH_CONTRACTS,
  groupPolicyFormValues,
  readGroupPolicies,
  SEARCH_CONTRACTS,
  TOOL_CONTRACTS,
  TOOL_ENDPOINTS,
  TOOL_POLICY_LABELS,
  updateGroupPolicies,
  type GroupPolicies,
  type GroupPolicyFormValues,
  type ToolContract,
} from '../lib/group-policies'
import { GroupPolicyMatrix } from './GroupPolicyMatrix'
import { ProviderOrderEditor } from './ProviderOrderEditor'
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

function readCapabilityState(
  rules: GroupPolicyFormValues['rules'] | undefined,
  contracts: ToolContract[]
) {
  const first = rules?.[contracts[0]] ?? { mode: 'inherit' as const, order: '' }
  const mixed = contracts.some((contract) => {
    const entry = rules?.[contract]
    return entry?.mode !== first.mode || entry?.order !== first.order
  })
  return { mode: first.mode, order: first.order, mixed }
}

function GroupPolicyForm(props: GroupPolicyFormProps) {
  const { t } = useTranslation()
  const groupId = useId()
  const titleId = useId()
  const providerHelpId = useId()
  const copyGroupId = useId()
  const queryClient = useQueryClient()
  const [conflictRules, setConflictRules] = useState<GroupPolicyFormValues>()
  const [advancedExpanded, setAdvancedExpanded] = useState<boolean | undefined>(
    undefined
  )
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
  const { isDirty, dirtyFields, isSubmitting, errors } = form.formState
  useResetForm(form, isDirty || isSubmitting ? undefined : defaults)

  const watchedRules = useWatch({ control: form.control, name: 'rules' })
  const searchState = readCapabilityState(watchedRules, SEARCH_CONTRACTS)
  const fetchState = readCapabilityState(watchedRules, FETCH_CONTRACTS)
  const hasPerProtocolDiff = searchState.mixed || fetchState.mixed
  const hasRuleErrors = !!errors.rules
  const showAdvanced =
    advancedExpanded ?? (hasPerProtocolDiff || hasRuleErrors)

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

  function applyCapabilityMode(
    contracts: ToolContract[],
    mode: GroupPolicyFormValues['rules'][ToolContract]['mode']
  ) {
    for (const contract of contracts) {
      form.setValue(`rules.${contract}.mode`, mode, { shouldDirty: true })
    }
  }

  function applyCapabilityOrder(contracts: ToolContract[], order: string) {
    for (const contract of contracts) {
      form.setValue(`rules.${contract}.order`, order, { shouldDirty: true })
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
        return (
          latestRule.mode !== originalRule?.mode ||
          latestRule.order !== originalRule?.order
        )
      })
      if (hasConflict) {
        setConflictRules(latest)
        return
      }
      const next = updateGroupPolicies(
        policies,
        props.group,
        values,
        dirtyFields.rules ?? {}
      )
      const saved = await savePolicySnapshot({
        value: JSON.stringify(next),
        version: snapshot.version,
      })
      queryClient.setQueryData(policiesKey, saved)
      form.reset(
        groupPolicyFormValues(readGroupPolicies(saved.value) ?? next, props.group)
      )
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
      <form aria-labelledby={titleId} onSubmit={form.handleSubmit(save)}>
        <Card className='@container'>
          <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-2'>
            <CardTitle>
              <h3 id={titleId}>{t('toolsApi.rulesByGroup')}</h3>
            </CardTitle>
            <RouteSimulator groups={props.groups} selectedGroup={props.group} />
          </CardHeader>
          <CardContent className='flex min-w-0 flex-col gap-4'>
            <GroupPolicyMatrix
              groups={props.groups}
              policies={props.policies}
              selectedGroup={props.group}
              onGroupChange={(next) => {
                setAdvancedExpanded(undefined)
                props.onGroupChange(next)
              }}
              disabled={isDirty || isSubmitting}
            />

            <div className='bg-muted/20 flex flex-wrap items-end gap-3 rounded-lg border p-3'>
              <Field
                data-disabled={isDirty || isSubmitting}
                className='min-w-[160px] flex-1'
              >
                <FieldLabel htmlFor={groupId}>{t('Group')}</FieldLabel>
                <NativeSelect
                  id={groupId}
                  name='group'
                  value={props.group}
                  disabled={isDirty || isSubmitting}
                  onChange={(event) => {
                    setAdvancedExpanded(undefined)
                    props.onGroupChange(event.target.value)
                  }}
                  className='w-full'
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
              <Field className='min-w-[180px] flex-1'>
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
                size='sm'
                disabled={isSubmitting}
                onClick={() =>
                  applyRules(groupPolicyFormValues({}, props.group))
                }
              >
                {t('Reset to inherited rules')}
              </Button>
            </div>

            {directoryQuery.isError && (
              <Alert variant='destructive'>
                <AlertDescription>
                  {t('Provider directory is unavailable. Existing selections are preserved.')}
                </AlertDescription>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={directoryQuery.isFetching}
                  onClick={() => void directoryQuery.refetch()}
                >
                  {t('Retry')}
                </Button>
              </Alert>
            )}

            {conflictRules && (
              <Alert>
                <AlertTitle>
                  {t('Group rules changed. Review your draft before retrying.')}
                </AlertTitle>
                <AlertDescription>
                  <p>{t('Current server rules')}</p>
                  <ul className='list-disc ps-5'>
                    {TOOL_CONTRACTS.filter((contract) => dirtyFields.rules?.[contract]).map(
                      (contract) => {
                        const rule = conflictRules.rules[contract]
                        return (
                          <li key={contract}>
                            {TOOL_ENDPOINTS[contract]}: {t(TOOL_POLICY_LABELS[rule.mode])}
                            {rule.order && ` · ${rule.order}`}
                          </li>
                        )
                      }
                    )}
                  </ul>
                </AlertDescription>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={keepDraft}
                >
                  {t('Keep draft')}
                </Button>
              </Alert>
            )}

            <div className='grid gap-4 @2xl:grid-cols-2'>
              <CapabilityPolicyCard
                title={t('Search')}
                description={t(
                  'Applies to /v1/tools/search, /v1/alpha/search, and /moonshot.service/v1/search.'
                )}
                icon={<Search className='text-muted-foreground size-4' />}
                contract='tools/search'
                state={searchState}
                disabled={isSubmitting}
                directory={directory}
                onModeChange={(mode) => applyCapabilityMode(SEARCH_CONTRACTS, mode)}
                onOrderChange={(order) =>
                  applyCapabilityOrder(SEARCH_CONTRACTS, order)
                }
              />
              <CapabilityPolicyCard
                title={t('Fetch')}
                description={t('toolsApi.fetchEndpoints', {
                  defaultValue: 'Applies to /v1/tools/fetch and /moonshot.service/v1/fetch.',
                })}
                icon={<Globe className='text-muted-foreground size-4' />}
                contract='tools/fetch'
                state={fetchState}
                disabled={isSubmitting}
                directory={directory}
                onModeChange={(mode) => applyCapabilityMode(FETCH_CONTRACTS, mode)}
                onOrderChange={(order) =>
                  applyCapabilityOrder(FETCH_CONTRACTS, order)
                }
              />
            </div>

            <p className='text-muted-foreground text-xs'>
              {t('toolsApi.channelFreeFallbackHelp', {
                defaultValue:
                  'Channel-free fallback is off by default. When enabled, if no eligible channel is available on the first attempt, Local Fetch (fetch only) or global Exa (with a key) may be used. The group policy, token and routing restrictions, and pricing still apply. Alpha Search is excluded.',
              })}
            </p>

            <Collapsible
              open={showAdvanced}
              onOpenChange={(open) => setAdvancedExpanded(open)}
            >
              <div className='rounded-lg border px-3.5 py-2.5'>
                <CollapsibleTrigger
                  render={
                    <button
                      type='button'
                      className='flex w-full items-center justify-between gap-2 py-1 text-left text-sm font-medium'
                    />
                  }
                >
                  <div className='flex items-center gap-2'>
                    <span>{t('Per-protocol overrides (Advanced)')}</span>
                    {hasPerProtocolDiff && (
                      <Badge variant='secondary' className='text-[11px]'>
                        {t('Per-protocol overrides')}
                      </Badge>
                    )}
                  </div>
                  <ChevronDown
                    className={cn(
                      'text-muted-foreground size-4 transition-transform',
                      showAdvanced && 'rotate-180'
                    )}
                    aria-hidden='true'
                  />
                </CollapsibleTrigger>
                <CollapsibleContent className='pt-3'>
                  <p
                    id={providerHelpId}
                    className='text-muted-foreground mb-2 text-xs'
                  >
                    {t('toolsApi.orderHelp')}
                  </p>
                  <div className='divide-y border-t'>
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
                </CollapsibleContent>
              </div>
            </Collapsible>
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
            >
              {t('Discard changes')}
            </Button>
            <Button
              type='submit'
              disabled={!isDirty || isSubmitting || !!conflictRules}
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

function CapabilityPolicyCard(props: {
  title: string
  description: string
  icon: React.ReactNode
  contract: ToolContract
  state: {
    mode: GroupPolicyFormValues['rules'][ToolContract]['mode']
    order: string
    mixed: boolean
  }
  disabled: boolean
  directory?: ProviderSummary[]
  onModeChange: (
    mode: GroupPolicyFormValues['rules'][ToolContract]['mode']
  ) => void
  onOrderChange: (order: string) => void
}) {
  const { t } = useTranslation()
  const titleId = useId()
  const modeHelpId = useId()

  function modeHelp(): string {
    if (props.state.mixed) {
      return t('toolsApi.mixedModeHelp', {
        defaultValue: 'Protocols use different rules. See per-protocol overrides.',
      })
    }
    switch (props.state.mode) {
      case 'inherit':
        if (props.contract === 'tools/fetch') {
          return t('toolsApi.inheritFetchHelp', {
            defaultValue:
              'Uses configured global routing defaults. The built-in Fetch order ranks eligible channel providers first, then Local Fetch, then global Exa when its key is configured. Global provider order can change this ranking.',
          })
        }
        return t('toolsApi.inheritSearchHelp', {
          defaultValue: 'Uses configured global routing defaults for Search.',
        })
      case 'disabled':
        return t('toolsApi.disabledModeHelp', {
          defaultValue: 'Blocks requests to these endpoints for this group.',
        })
      case 'prefer':
        return t('toolsApi.preferModeHelp', {
          defaultValue:
            'Ranks listed eligible providers first. Unlisted eligible providers can still be used.',
        })
      case 'only':
        return t('toolsApi.onlyModeHelp', {
          defaultValue:
            'Uses only listed providers that are eligible. Listing a provider does not make it eligible.',
        })
    }
  }

  return (
    <div className='flex flex-col gap-3 rounded-lg border p-3.5'>
      <div className='flex items-start justify-between gap-2'>
        <div className='flex flex-col gap-0.5'>
          <h4 id={titleId} className='flex items-center gap-1.5 text-sm font-medium'>
            {props.icon}
            {props.title}
          </h4>
          <p className='text-muted-foreground text-xs'>{props.description}</p>
        </div>
        {props.state.mixed && (
          <Badge variant='outline' className='shrink-0 text-[11px]'>
            {t('Mixed')}
          </Badge>
        )}
      </div>

      <div className='flex flex-col gap-1.5'>
        <ToggleGroup
          aria-labelledby={titleId}
          aria-describedby={modeHelpId}
          disabled={props.disabled}
          value={props.state.mixed ? [] : [props.state.mode]}
          onValueChange={(value) => {
            if (value[0]) {
              props.onModeChange(
                value[0] as GroupPolicyFormValues['rules'][ToolContract]['mode']
              )
            }
          }}
          variant='outline'
          className='flex flex-wrap'
        >
          <ToggleGroupItem value='inherit' className='h-8 text-xs'>
            {t('toolsApi.inherit')}
          </ToggleGroupItem>
          <ToggleGroupItem value='disabled' className='h-8 text-xs'>
            {t('Disabled')}
          </ToggleGroupItem>
          <ToggleGroupItem value='prefer' className='h-8 text-xs'>
            {t('toolsApi.prefer')}
          </ToggleGroupItem>
          <ToggleGroupItem value='only' className='h-8 text-xs'>
            {t('Only listed providers')}
          </ToggleGroupItem>
        </ToggleGroup>
        <p id={modeHelpId} role='status' className='text-muted-foreground text-xs'>
          {modeHelp()}
        </p>
      </div>

      {!props.state.mixed &&
        (props.state.mode === 'prefer' || props.state.mode === 'only') && (
          <ProviderOrderEditor
            value={props.state.order}
            onChange={props.onOrderChange}
            contract={props.contract}
            directory={props.directory}
            disabled={props.disabled}
          />
        )}
    </div>
  )
}
