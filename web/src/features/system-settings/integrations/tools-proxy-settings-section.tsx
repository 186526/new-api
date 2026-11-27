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
import { useEffect, useId, useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

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
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

// Provider names the backend accepts as a candidate. Kept in sync with
// setting/websearch_setting.KnownProviders.
const knownProviders = [
  'antigravity',
  'codex',
  'deepseek',
  'exa',
  'local',
] as const

function findUnknownProvider(order: string): string | null {
  for (const entry of order.split(',')) {
    const name = entry.trim().toLowerCase()
    if (!name) continue
    if (!knownProviders.includes(name as (typeof knownProviders)[number])) {
      return name
    }
  }
  return null
}

const createToolsProxySchema = (t: (key: string) => string) =>
  z.object({
    websearch_setting: z.object({
      candidate_order: z.string().superRefine((value, ctx) => {
        const unknown = findUnknownProvider(value)
        if (unknown !== null) {
          ctx.addIssue({
            code: 'custom',
            message: `${t('Unknown provider')}: ${unknown}`,
          })
        }
      }),
      native_first: z.boolean(),
      search_cache_ttl_seconds: z.number().int().min(-1),
      fetch_cache_ttl_seconds: z.number().int().min(-1),
      redirect_cache_ttl_seconds: z.number().int().min(-1),
      response_header_timeout_seconds: z.number().int().min(0),
      retry_attempts: z.number().int().min(0).max(10),
      retry_base_delay_milliseconds: z.number().int().min(0),
      exa_base_url: z.string().refine((value) => {
        const trimmed = value.trim()
        if (!trimmed) return true
        return /^https?:\/\//.test(trimmed)
      }, t('Provide a valid URL starting with http:// or https://')),
      exa_api_key: z.string(),
      antigravity_search_model: z.string(),
    }),
  })

type ToolsProxyFormValues = z.infer<ReturnType<typeof createToolsProxySchema>>

// Flat keys as the option API stores them. The form works on the nested shape
// above and these three helpers translate between the two.
type FlatToolsProxySettings = {
  'websearch_setting.candidate_order': string
  'websearch_setting.native_first': boolean
  'websearch_setting.search_cache_ttl_seconds': number
  'websearch_setting.fetch_cache_ttl_seconds': number
  'websearch_setting.redirect_cache_ttl_seconds': number
  'websearch_setting.response_header_timeout_seconds': number
  'websearch_setting.retry_attempts': number
  'websearch_setting.retry_base_delay_milliseconds': number
  'websearch_setting.exa_base_url': string
  'websearch_setting.exa_api_key': string
  'websearch_setting.antigravity_search_model': string
}

type ToolsProxySectionDefaults = FlatToolsProxySettings & {
  // The option API never returns exa_api_key; it reports whether one is stored.
  ExaKeyConfigured?: boolean
}

type ToolsProxySettingsSectionProps = {
  defaultValues: ToolsProxySectionDefaults
}

const buildFormDefaults = (
  defaults: ToolsProxySettingsSectionProps['defaultValues']
): ToolsProxyFormValues => ({
  websearch_setting: {
    candidate_order: defaults['websearch_setting.candidate_order'] ?? '',
    native_first: defaults['websearch_setting.native_first'] ?? true,
    search_cache_ttl_seconds:
      defaults['websearch_setting.search_cache_ttl_seconds'] ?? 600,
    fetch_cache_ttl_seconds:
      defaults['websearch_setting.fetch_cache_ttl_seconds'] ?? 3600,
    redirect_cache_ttl_seconds:
      defaults['websearch_setting.redirect_cache_ttl_seconds'] ?? 86400,
    response_header_timeout_seconds:
      defaults['websearch_setting.response_header_timeout_seconds'] ?? 25,
    retry_attempts: defaults['websearch_setting.retry_attempts'] ?? 3,
    retry_base_delay_milliseconds:
      defaults['websearch_setting.retry_base_delay_milliseconds'] ?? 500,
    exa_base_url: defaults['websearch_setting.exa_base_url'] ?? '',
    exa_api_key: '',
    antigravity_search_model:
      defaults['websearch_setting.antigravity_search_model'] ?? '',
  },
})

const normalizeDefaults = (
  defaults: ToolsProxySettingsSectionProps['defaultValues']
): FlatToolsProxySettings => ({
  'websearch_setting.candidate_order':
    defaults['websearch_setting.candidate_order'] ?? '',
  'websearch_setting.native_first':
    defaults['websearch_setting.native_first'] ?? true,
  'websearch_setting.search_cache_ttl_seconds':
    defaults['websearch_setting.search_cache_ttl_seconds'] ?? 600,
  'websearch_setting.fetch_cache_ttl_seconds':
    defaults['websearch_setting.fetch_cache_ttl_seconds'] ?? 3600,
  'websearch_setting.redirect_cache_ttl_seconds':
    defaults['websearch_setting.redirect_cache_ttl_seconds'] ?? 86400,
  'websearch_setting.response_header_timeout_seconds':
    defaults['websearch_setting.response_header_timeout_seconds'] ?? 25,
  'websearch_setting.retry_attempts':
    defaults['websearch_setting.retry_attempts'] ?? 3,
  'websearch_setting.retry_base_delay_milliseconds':
    defaults['websearch_setting.retry_base_delay_milliseconds'] ?? 500,
  'websearch_setting.exa_base_url':
    defaults['websearch_setting.exa_base_url'] ?? '',
  // The stored key is never readable from here, so the baseline is empty and a
  // newly typed key always counts as a change.
  'websearch_setting.exa_api_key': '',
  'websearch_setting.antigravity_search_model':
    defaults['websearch_setting.antigravity_search_model'] ?? '',
})

const normalizeFormValues = (
  values: ToolsProxyFormValues
): FlatToolsProxySettings => ({
  'websearch_setting.candidate_order':
    values.websearch_setting.candidate_order.trim(),
  'websearch_setting.native_first': values.websearch_setting.native_first,
  'websearch_setting.search_cache_ttl_seconds':
    values.websearch_setting.search_cache_ttl_seconds,
  'websearch_setting.fetch_cache_ttl_seconds':
    values.websearch_setting.fetch_cache_ttl_seconds,
  'websearch_setting.redirect_cache_ttl_seconds':
    values.websearch_setting.redirect_cache_ttl_seconds,
  'websearch_setting.response_header_timeout_seconds':
    values.websearch_setting.response_header_timeout_seconds,
  'websearch_setting.retry_attempts': values.websearch_setting.retry_attempts,
  'websearch_setting.retry_base_delay_milliseconds':
    values.websearch_setting.retry_base_delay_milliseconds,
  'websearch_setting.exa_base_url':
    values.websearch_setting.exa_base_url.trim(),
  'websearch_setting.exa_api_key': values.websearch_setting.exa_api_key.trim(),
  'websearch_setting.antigravity_search_model':
    values.websearch_setting.antigravity_search_model.trim(),
})

export function ToolsProxySettingsSection({
  defaultValues,
}: ToolsProxySettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const orderTitleId = useId()
  const cacheTitleId = useId()
  const upstreamTitleId = useId()

  const baselineRef = useRef<FlatToolsProxySettings>(
    normalizeDefaults(defaultValues)
  )
  const baselineSerializedRef = useRef<string>(
    JSON.stringify(normalizeDefaults(defaultValues))
  )

  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues),
    [defaultValues]
  )

  const toolsProxySchema = useMemo(() => createToolsProxySchema(t), [t])

  const form = useForm<ToolsProxyFormValues>({
    resolver: zodResolver(toolsProxySchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  useEffect(() => {
    const normalized = normalizeDefaults(defaultValues)
    const serialized = JSON.stringify(normalized)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = normalized
    baselineSerializedRef.current = serialized
  }, [defaultValues])

  const exaKeyConfigured = defaultValues.ExaKeyConfigured === true

  const onSubmit = async (values: ToolsProxyFormValues) => {
    const normalized = normalizeFormValues(values)
    const updates = (
      Object.keys(normalized) as Array<keyof FlatToolsProxySettings>
    ).filter((key) => {
      if (normalized[key] === baselineRef.current[key]) return false
      // An empty key field means "leave the stored key alone".
      if (key === 'websearch_setting.exa_api_key' && normalized[key] === '') {
        return false
      }
      return true
    })

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const key of updates) {
      await updateOption.mutateAsync({
        key,
        value: normalized[key],
      })
    }

    // The key field is write-only, so the baseline never holds a key: clearing
    // it here keeps a second entry of the same value from looking unchanged.
    const nextBaseline: FlatToolsProxySettings = {
      ...normalized,
      'websearch_setting.exa_api_key': '',
    }
    baselineRef.current = nextBaseline
    baselineSerializedRef.current = JSON.stringify(nextBaseline)
    form.setValue('websearch_setting.exa_api_key', '')
  }

  return (
    <SettingsSection title={t('Tools Proxy')} className='gap-6'>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save Tools Proxy settings'
          />

          <div
            role='group'
            aria-labelledby={orderTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={orderTitleId} className='font-medium'>
              {t('Candidate order')}
            </h4>
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='websearch_setting.candidate_order'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Provider order')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder='exa,deepseek,antigravity,local'
                        autoComplete='off'
                        {...field}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Comma-separated providers to try in order. Leave blank to use the built-in order.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='websearch_setting.native_first'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Prefer native providers')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Keep the provider whose upstream speaks the client contract ahead of the rest. Turn off to follow only the candidate order.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
            </SettingsFormGrid>
          </div>

          <Separator />

          <div
            role='group'
            aria-labelledby={cacheTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={cacheTitleId} className='font-medium'>
              {t('Cache and retries')}
            </h4>
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='websearch_setting.search_cache_ttl_seconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Search cache TTL (seconds)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={-1}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        '0 uses the built-in 600 seconds. -1 disables the search cache.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='websearch_setting.fetch_cache_ttl_seconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Fetch cache TTL (seconds)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={-1}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        '0 uses the built-in 3600 seconds. -1 disables the fetch cache.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='websearch_setting.redirect_cache_ttl_seconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Redirect cache TTL (seconds)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={-1}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        '0 uses the built-in 86400 seconds. -1 disables the redirect cache.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='websearch_setting.response_header_timeout_seconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Response header timeout (seconds)')}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('0 uses the built-in 25 seconds.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='websearch_setting.retry_attempts'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Retry attempts')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        max={10}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        '0 uses the built-in 3 attempts. Allowed range is 0 to 10.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='websearch_setting.retry_base_delay_milliseconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Retry base delay (milliseconds)')}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('0 uses the built-in 500 milliseconds.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGrid>
          </div>

          <Separator />

          <div
            role='group'
            aria-labelledby={upstreamTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={upstreamTitleId} className='font-medium'>
              {t('Upstreams')}
            </h4>
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='websearch_setting.exa_base_url'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Exa base URL')}</FormLabel>
                    <FormControl>
                      <Input
                        type='url'
                        inputMode='url'
                        placeholder='https://api.exa.ai'
                        autoComplete='off'
                        {...field}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Leave blank to use https://api.exa.ai.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='websearch_setting.exa_api_key'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Exa API key')}</FormLabel>
                    <FormControl>
                      <Input
                        type='password'
                        placeholder={t('Enter new key to update')}
                        autoComplete='new-password'
                        {...field}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {exaKeyConfigured
                        ? t('Configured. Leave blank to keep the current key.')
                        : t(
                            'Leave blank to let only channels bound to Exa use it.'
                          )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='websearch_setting.antigravity_search_model'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Antigravity search model')}</FormLabel>
                    <FormControl>
                      <Input
                        autoComplete='off'
                        placeholder={t('Leave blank to use the built-in model')}
                        {...field}
                        onChange={(event) => field.onChange(event.target.value)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Model an Antigravity search is sent to. Leave blank to use the built-in model name.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGrid>
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
