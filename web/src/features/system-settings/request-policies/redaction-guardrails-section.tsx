import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect, useId } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

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
import { Switch } from '@/components/ui/switch'

import {
  SettingsControlGroup,
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { useSavePolicy } from './use-save-policy'

const schema = z.object({
  enabled: z.boolean(),
  maxCustomRules: z.number().int().min(0).max(10),
  allowResponseRestore: z.boolean(),
})

type Guardrails = z.infer<typeof schema>

const optionKeys = {
  enabled: 'redaction_setting.enabled',
  maxCustomRules: 'redaction_setting.max_custom_rules_per_key',
  allowResponseRestore: 'redaction_setting.allow_response_restore',
} as const

export function RedactionGuardrailsSection({
  enabled,
  maxCustomRules,
  allowResponseRestore,
}: Guardrails) {
  const { t } = useTranslation()
  const titleId = useId()
  const save = useSavePolicy()
  const form = useForm<Guardrails>({
    resolver: zodResolver(schema),
    defaultValues: { enabled, maxCustomRules, allowResponseRestore },
  })
  const {
    reset,
    formState: { dirtyFields, isSubmitting },
  } = form
  useEffect(() => {
    reset(
      { enabled, maxCustomRules, allowResponseRestore },
      { keepDirtyValues: true }
    )
  }, [reset, enabled, maxCustomRules, allowResponseRestore])

  const submit = form.handleSubmit(async (values) => {
    const changed = (Object.keys(optionKeys) as Array<keyof Guardrails>).filter(
      (name) => dirtyFields[name]
    )
    if (changed.length === 0) return
    try {
      await save.mutateAsync(
        Object.fromEntries(
          changed.map((name) => [optionKeys[name], String(values[name])])
        )
      )
      reset(values)
    } catch {
      // The shared mutation reports the error; keep the draft.
    }
  })

  return (
    <section className='flex flex-col gap-4'>
      <h3 id={titleId} className='text-base font-semibold'>
        {t('Request redaction guardrails')}
      </h3>
      <Form {...form}>
        <SettingsForm aria-labelledby={titleId} onSubmit={submit} noValidate>
          <SettingsControlGroup>
            {(
              [
                [
                  'enabled',
                  t('Allow request redaction'),
                  t('Allow users to enable request redaction per API key.'),
                ],
                [
                  'allowResponseRestore',
                  t('Allow response restoration'),
                  t(
                    'Only opted-in keys can restore personal information. Credentials are never restored.'
                  ),
                ],
              ] as const
            ).map(([name, label, description]) => (
              <FormField
                key={name}
                name={name}
                control={form.control}
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{label}</FormLabel>
                      <FormDescription>{description}</FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        name={field.name}
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={isSubmitting}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
            ))}
            <FormField
              name='maxCustomRules'
              control={form.control}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Custom rules per key')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='number'
                      min={0}
                      max={10}
                      step={1}
                      disabled={isSubmitting}
                      value={Number.isNaN(field.value) ? '' : field.value}
                      onChange={(event) =>
                        field.onChange(event.target.valueAsNumber)
                      }
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Enter 0–10. Zero disables custom rules.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsControlGroup>
          <Button type='submit' className='self-start' disabled={isSubmitting}>
            {t('Save redaction settings')}
          </Button>
        </SettingsForm>
      </Form>
    </section>
  )
}
