import { zodResolver } from '@hookform/resolvers/zod'
import { Link } from '@tanstack/react-router'
import { useEffect } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

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
import { Textarea } from '@/components/ui/textarea'

import { SettingsForm } from '../components/settings-form-layout'
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
          <FormField
            control={control}
            name='enabled'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Enable automatic reasoning')}</FormLabel>
                <FormControl>
                  <Switch
                    name={field.name}
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Model suffixes take precedence. Pass-through requests are not changed.'
                  )}
                </FormDescription>
              </FormItem>
            )}
          />
          <fieldset
            className='flex flex-col gap-4'
            disabled={!enabled || updateOption.isPending}
          >
            <legend>{t('Reasoning rules')}</legend>
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                variant='outline'
                onClick={() =>
                  applyPreset(
                    [3000, 12000, 32000],
                    ['minimal', 'low', 'medium']
                  )
                }
              >
                {t('Economical chat')}
              </Button>
              <Button
                type='button'
                variant='outline'
                onClick={() =>
                  applyPreset([1500, 8000, 32000], ['low', 'medium', 'high'])
                }
              >
                {t('Balanced coding')}
              </Button>
              <Button
                type='button'
                variant='outline'
                onClick={() =>
                  applyPreset([1000, 6000, 24000], ['medium', 'high', 'xhigh'])
                }
              >
                {t('Deep analysis')}
              </Button>
            </div>
            <FormField
              control={control}
              name='scale_metric'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Request size metric')}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field}>
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
                    <NativeSelect {...field}>
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
            <FormField
              control={control}
              name='respect_explicit_disable'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Respect explicit reasoning disable')}
                  </FormLabel>
                  <FormControl>
                    <Switch
                      name={field.name}
                      checked={field.value}
                      onCheckedChange={field.onChange}
                      disabled={!enabled}
                    />
                  </FormControl>
                </FormItem>
              )}
            />
            {rows.fields.map((row, index) => (
              <fieldset key={row.id} className='flex flex-wrap items-end gap-3'>
                <legend>
                  {t('Reasoning tier {{index}}', { index: index + 1 })}
                </legend>
                <FormField
                  control={control}
                  name={`tiers.${index}.max_scale`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Maximum input size ({{unit}})', { unit })}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={1}
                          max={1_000_000_000}
                          inputMode='numeric'
                          required
                          value={Number.isNaN(field.value) ? '' : field.value}
                          onChange={(event) =>
                            field.onChange(event.currentTarget.valueAsNumber)
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <EffortField control={control} name={`tiers.${index}.effort`} />
                <Button
                  type='button'
                  variant='outline'
                  disabled={rows.fields.length === 1}
                  onClick={() => rows.remove(index)}
                >
                  {t('Remove')}
                </Button>
              </fieldset>
            ))}
            <Button
              className='self-start'
              type='button'
              variant='outline'
              disabled={rows.fields.length >= 6}
              onClick={() => {
                const previous = form.getValues('tiers').at(-1)?.max_scale || 0
                rows.append({
                  max_scale: Math.min(previous + 1000, 1_000_000_000),
                  effort: 'high',
                })
              }}
            >
              {t('Add tier')}
            </Button>
            <EffortField control={control} name='overflow_effort' />
            <ScopeFields control={control} />

          </fieldset>
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
}) {
  const { t } = useTranslation()
  return (
    <FormField
      control={props.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>
            {props.name === 'overflow_effort'
              ? t('Overflow effort')
              : t('Reasoning effort')}
          </FormLabel>
          <FormControl>
            <NativeSelect {...field}>
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

function ScopeFields(props: { control: PolicyControl }) {
  const { t } = useTranslation()
  const fields = [
    { name: 'groups', label: t('Allowed groups') },
    { name: 'model_patterns', label: t('Included model patterns') },
    { name: 'exclude_model_patterns', label: t('Excluded model patterns') },
  ] as const
  return (
    <fieldset className='flex flex-col gap-3'>
      <legend>{t('Scope')}</legend>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Enter one value per line. Model patterns accept prefixes, wildcards, or re: expressions.'
        )}
      </p>
      {fields.map((item) => (
        <FormField
          key={item.name}
          control={props.control}
          name={item.name}
          render={({ field }) => (
            <FormItem>
              <FormLabel>{item.label}</FormLabel>
              <FormControl>
                <Textarea {...field} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      ))}
    </fieldset>
  )
}
