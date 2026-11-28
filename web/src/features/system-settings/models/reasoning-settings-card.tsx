import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Plus, Trash2 } from 'lucide-react'
import { useEffect } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { getGroups } from '@/features/users/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  SettingsControlGroup,
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  AUTO_EFFORT_KEY,
  autoReasoningFormSchema,
  conflictPolicies,
  defaultAutoReasoningPolicy,
  efforts,
  parseAutoReasoningPolicy,
  policyToForm,
  scaleMetrics,
  type AutoReasoningForm,
  type AutoReasoningPolicy,
} from './auto-reasoning-policy'
import { AutoReasoningPreview } from './auto-reasoning-preview'

function usePolicyForm(value: string) {
  const form = useForm<AutoReasoningForm>({
    resolver: zodResolver(autoReasoningFormSchema, undefined, { raw: true }),
    defaultValues: policyToForm(
      parseAutoReasoningPolicy(value) ?? defaultAutoReasoningPolicy
    ),
    mode: 'onTouched',
  })
  const { reset } = form
  useEffect(() => {
    reset(
      policyToForm(
        parseAutoReasoningPolicy(value) ?? defaultAutoReasoningPolicy
      )
    )
  }, [value, reset])
  return form
}

export function ReasoningSettingsCard(props: { value: string }) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const form = usePolicyForm(props.value)
  const { reset, control } = form
  const rows = useFieldArray({ control, name: 'tiers' })
  const enabled = form.watch('enabled')
  const metric = form.watch('scale_metric')
  const unit = metric === 'input_chars' ? t('Characters') : t('Tokens')
  const parsed = autoReasoningFormSchema.safeParse(form.watch())
  const save = form.handleSubmit(async (draft) => {
    const policy = autoReasoningFormSchema.parse(draft)
    try {
      await updateOption.mutateAsync({
        key: AUTO_EFFORT_KEY,
        value: JSON.stringify(policy),
      })
      reset(policyToForm(policy))
    } catch {
      // The mutation reports the error and leaves the draft intact.
    }
  })
  const applyPreset = (
    thresholds: [number, number, number],
    levels: [
      AutoReasoningPolicy['overflow_effort'],
      AutoReasoningPolicy['overflow_effort'],
      AutoReasoningPolicy['overflow_effort'],
    ]
  ) => {
    rows.replace(
      thresholds.map((max_scale, index) => ({
        max_scale,
        effort: levels[index],
      }))
    )
    form.setValue('overflow_effort', levels[2], {
      shouldDirty: true,
      shouldValidate: true,
    })
  }

  const tiers = form.watch('tiers')
  const overflowEffort = form.watch('overflow_effort')
  let activePreset: 'economical' | 'balanced' | 'deep' | null = null
  if (
    tiers.length === 3 &&
    tiers[0]?.max_scale === 3000 &&
    tiers[0]?.effort === 'minimal' &&
    tiers[1]?.max_scale === 12000 &&
    tiers[2]?.max_scale === 32000 &&
    overflowEffort === 'medium'
  ) {
    activePreset = 'economical'
  } else if (
    tiers.length === 3 &&
    tiers[0]?.max_scale === 1500 &&
    tiers[0]?.effort === 'low' &&
    tiers[1]?.max_scale === 8000 &&
    tiers[2]?.max_scale === 32000 &&
    overflowEffort === 'high'
  ) {
    activePreset = 'balanced'
  } else if (
    tiers.length === 3 &&
    tiers[0]?.max_scale === 1000 &&
    tiers[0]?.effort === 'medium' &&
    tiers[1]?.max_scale === 6000 &&
    tiers[2]?.max_scale === 24000 &&
    overflowEffort === 'xhigh'
  ) {
    activePreset = 'deep'
  }

  return (
    <SettingsSection title={t('Automatic reasoning')}>
      <div className='text-primary flex flex-wrap gap-4 text-sm'>
        <Link className='underline underline-offset-4' to='/system-settings/models/$section' params={{ section: 'global' }}>
          {t('Thinking model blacklist')}
        </Link>
        <Link className='underline underline-offset-4' to='/system-settings/models/$section' params={{ section: 'claude' }}>
          {t('Claude thinking budget')}
        </Link>
      </div>
      <Form {...form}>
        <SettingsForm onSubmit={save} noValidate>
          <SettingsPageFormActions
            onSave={save}
            isSaving={updateOption.isPending}
          />

          <SettingsControlGroup className='space-y-4'>
            <FormField
              control={control}
              name='enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Enable automatic reasoning')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Model suffixes take precedence. Pass-through requests are not changed.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      name={field.name}
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />

            <div className='grid gap-2 sm:grid-cols-3'>
              <Button
                type='button'
                variant={activePreset === 'economical' ? 'secondary' : 'outline'}
                className='h-auto flex-col items-start gap-0.5 px-3 py-2.5 text-left'
                disabled={!enabled || updateOption.isPending}
                onClick={() =>
                  applyPreset([3000, 12000, 32000], ['minimal', 'low', 'medium'])
                }
              >
                <span className='text-sm font-medium'>{t('Economical chat')}</span>
                <span className='text-muted-foreground text-xs font-normal'>
                  minimal → low → medium
                </span>
              </Button>
              <Button
                type='button'
                variant={activePreset === 'balanced' ? 'secondary' : 'outline'}
                className='h-auto flex-col items-start gap-0.5 px-3 py-2.5 text-left'
                disabled={!enabled || updateOption.isPending}
                onClick={() =>
                  applyPreset([1500, 8000, 32000], ['low', 'medium', 'high'])
                }
              >
                <span className='text-sm font-medium'>{t('Balanced coding')}</span>
                <span className='text-muted-foreground text-xs font-normal'>
                  low → medium → high
                </span>
              </Button>
              <Button
                type='button'
                variant={activePreset === 'deep' ? 'secondary' : 'outline'}
                className='h-auto flex-col items-start gap-0.5 px-3 py-2.5 text-left'
                disabled={!enabled || updateOption.isPending}
                onClick={() =>
                  applyPreset([1000, 6000, 24000], ['medium', 'high', 'xhigh'])
                }
              >
                <span className='text-sm font-medium'>{t('Deep analysis')}</span>
                <span className='text-muted-foreground text-xs font-normal'>
                  medium → high → xhigh
                </span>
              </Button>
            </div>

            <div className='grid gap-4 sm:grid-cols-2'>
              <FormField
                control={control}
                name='scale_metric'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Request size metric')}</FormLabel>
                    <FormControl>
                      <NativeSelect
                        {...field}
                        disabled={!enabled || updateOption.isPending}
                      >
                        <NativeSelectOption value={scaleMetrics[0]}>
                          {t('Estimated input tokens')}
                        </NativeSelectOption>
                        <NativeSelectOption value={scaleMetrics[1]}>
                          {t('Input characters')}
                        </NativeSelectOption>
                      </NativeSelect>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={control}
                name='conflict_policy'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Client reasoning settings')}</FormLabel>
                    <FormControl>
                      <NativeSelect
                        {...field}
                        disabled={!enabled || updateOption.isPending}
                      >
                        <NativeSelectOption value={conflictPolicies[0]}>
                          {t('Fill only when unset')}
                        </NativeSelectOption>
                        <NativeSelectOption value={conflictPolicies[1]}>
                          {t('Cap at automatic effort')}
                        </NativeSelectOption>
                        <NativeSelectOption value={conflictPolicies[2]}>
                          {t('Override client effort')}
                        </NativeSelectOption>
                      </NativeSelect>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={control}
              name='respect_explicit_disable'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t('Respect explicit reasoning disable')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Keep requests with none or disabled reasoning unchanged.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      name={field.name}
                      checked={field.value}
                      onCheckedChange={field.onChange}
                      disabled={!enabled || updateOption.isPending}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </SettingsControlGroup>

          <SettingsControlGroup className='space-y-4'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <h3 className='text-sm font-medium'>{t('Reasoning rules')}</h3>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Thresholds are evaluated in ascending order of input size.'
                  )}
                </p>
              </div>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!enabled || updateOption.isPending || rows.fields.length >= 6}
                onClick={() => {
                  const previous = form.getValues('tiers').at(-1)?.max_scale || 0
                  rows.append({
                    max_scale: Math.min(previous + 1000, 1_000_000_000),
                    effort: 'high',
                  })
                }}
              >
                <Plus className='mr-1.5 size-3.5' aria-hidden='true' />
                {t('Add tier')}
              </Button>
            </div>

            <div className='overflow-x-auto rounded-lg border'>
              <Table withContainer={false}>
                <TableHeader className='bg-muted/40'>
                  <TableRow>
                    <TableHead className='w-32 text-xs'>{t('Tier')}</TableHead>
                    <TableHead className='text-xs'>
                      {t('Maximum input size ({{unit}})', { unit })}
                    </TableHead>
                    <TableHead className='w-40 text-xs'>
                      {t('Reasoning effort')}
                    </TableHead>
                    <TableHead className='w-14 text-right text-xs' />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.fields.map((row, index) => {
                    const prevScale =
                      index === 0 ? 0 : Number(tiers[index - 1]?.max_scale) || 0
                    const currentScale = Number(tiers[index]?.max_scale)
                    const rangeText = Number.isFinite(currentScale)
                      ? `${prevScale === 0 ? 0 : prevScale + 1} – ${currentScale}`
                      : `#${index + 1}`
                    return (
                      <TableRow key={row.id}>
                        <TableCell className='text-muted-foreground font-mono text-xs tabular-nums'>
                          {rangeText}
                        </TableCell>
                        <TableCell>
                          <FormField
                            control={control}
                            name={`tiers.${index}.max_scale`}
                            render={({ field }) => (
                              <FormItem className='space-y-1'>
                                <FormControl>
                                  <Input
                                    {...field}
                                    type='number'
                                    min={1}
                                    max={1_000_000_000}
                                    inputMode='numeric'
                                    required
                                    disabled={!enabled || updateOption.isPending}
                                    aria-label={t('Reasoning tier {{index}}', {
                                      index: index + 1,
                                    })}
                                    value={Number.isNaN(field.value) ? '' : field.value}
                                    onChange={(event) =>
                                      field.onChange(
                                        event.currentTarget.valueAsNumber
                                      )
                                    }
                                    className='h-8'
                                  />
                                </FormControl>
                                <FormMessage />
                              </FormItem>
                            )}
                          />
                        </TableCell>
                        <TableCell>
                          <EffortField
                            control={control}
                            name={`tiers.${index}.effort`}
                            disabled={!enabled || updateOption.isPending}
                            hideLabel
                          />
                        </TableCell>
                        <TableCell className='text-right'>
                          <Button
                            type='button'
                            variant='ghost'
                            size='icon-xs'
                            aria-label={`${t('Remove')} ${t('Reasoning tier {{index}}', { index: index + 1 })}`}
                            disabled={
                              !enabled ||
                              updateOption.isPending ||
                              rows.fields.length === 1
                            }
                            className='text-muted-foreground hover:text-destructive'
                            onClick={() => rows.remove(index)}
                          >
                            <Trash2 className='size-3.5' aria-hidden='true' />
                          </Button>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                  <TableRow className='bg-muted/20'>
                    <TableCell className='text-muted-foreground text-xs font-medium' colSpan={2}>
                      {t('Overflow effort')}
                    </TableCell>
                    <TableCell>
                      <EffortField
                        control={control}
                        name='overflow_effort'
                        disabled={!enabled || updateOption.isPending}
                        hideLabel
                      />
                    </TableCell>
                    <TableCell />
                  </TableRow>
                </TableBody>
              </Table>
            </div>
          </SettingsControlGroup>

          <ScopeFields
            control={control}
            disabled={!enabled || updateOption.isPending}
          />
        </SettingsForm>
      </Form>
      <AutoReasoningPreview policy={parsed.data} unit={unit} />
    </SettingsSection>
  )
}

type PolicyControl = ReturnType<typeof usePolicyForm>['control']

function EffortField(props: {
  control: PolicyControl
  name: 'overflow_effort' | `tiers.${number}.effort`
  disabled?: boolean
  hideLabel?: boolean
}) {
  const { t } = useTranslation()
  const label =
    props.name === 'overflow_effort'
      ? t('Overflow effort')
      : t('Reasoning effort')
  return (
    <FormField
      control={props.control}
      name={props.name}
      render={({ field }) => (
        <FormItem className={props.hideLabel ? 'space-y-0' : undefined}>
          {!props.hideLabel && <FormLabel>{label}</FormLabel>}
          <FormControl>
            <NativeSelect
              {...field}
              aria-label={props.hideLabel ? label : undefined}
              disabled={props.disabled}
              className={props.hideLabel ? 'h-8' : undefined}
            >
              {efforts.map((effort) => (
                <NativeSelectOption key={effort} value={effort}>
                  {effort}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </FormControl>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

function ScopeFields(props: { control: PolicyControl; disabled?: boolean }) {
  const { t } = useTranslation()
  const groupQuery = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
    staleTime: 60_000,
    meta: { errorToast: false },
  })
  const availableGroups = [...new Set(groupQuery.data?.data ?? [])].sort()

  return (
    <SettingsControlGroup className='space-y-4'>
      <div>
        <h3 className='text-sm font-medium'>{t('Scope')}</h3>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Enter one value per line. Model patterns accept prefixes, wildcards, or re: expressions.'
          )}
        </p>
      </div>
      <FormField
        control={props.control}
        name='groups'
        render={({ field }) => {
          const selected = String(field.value ?? '')
            .split('\n')
            .map((line) => line.trim())
            .filter(Boolean)
          const options = [...new Set([...availableGroups, ...selected])].map(
            (group) => ({ label: group, value: group })
          )
          return (
            <FormItem>
              <FormLabel>{t('Allowed groups')}</FormLabel>
              <FormControl>
                {options.length > 0 ? (
                  <MultiSelect
                    options={options}
                    selected={selected}
                    onChange={(next) => field.onChange(next.join('\n'))}
                    placeholder={t('All groups when empty')}
                    disabled={props.disabled}
                  />
                ) : (
                  <Textarea
                    {...field}
                    rows={2}
                    disabled={props.disabled}
                    placeholder={t('All groups when empty')}
                  />
                )}
              </FormControl>
              <FormMessage />
            </FormItem>
          )
        }}
      />
      <div className='grid gap-4 sm:grid-cols-2'>
        {(
          [
            { name: 'model_patterns', label: t('Included model patterns') },
            { name: 'exclude_model_patterns', label: t('Excluded model patterns') },
          ] as const
        ).map((item) => (
          <FormField
            key={item.name}
            control={props.control}
            name={item.name}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{item.label}</FormLabel>
                <FormControl>
                  <Textarea {...field} rows={3} disabled={props.disabled} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        ))}
      </div>
    </SettingsControlGroup>
  )
}
