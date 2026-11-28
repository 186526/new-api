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
import { ChevronDown, Database } from 'lucide-react'
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
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
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'
import { ToolsApiContent } from '@/features/tools-api'
import { ProviderOrderEditor } from '@/features/tools-api/components/ProviderOrderEditor'
import { ToolsCache } from '@/features/tools-api/components/ToolsCache'
import { ToolsDiagnostics } from '@/features/tools-api/components/ToolsDiagnostics'
import { ToolsNavigation } from '@/features/tools-api/components/ToolsNavigation'
import { getProviderSummary, summaryKey, type ProviderSummary } from '@/features/tools-api/lib/admin'
import {
  parseProviderOrder,
  supportsToolContract,
} from '@/features/tools-api/lib/group-policies'

import {
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useSystemOptions } from '../hooks/use-system-options'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

const createToolsProxySchema = (t: (key: string) => string, directory?: ProviderSummary[]) =>
  z.object({
    websearch_setting: z.object({
      candidate_order: z.string().superRefine((value, ctx) => {
        const providers = parseProviderOrder(value)
        const unknown = providers.find(
          (provider) => !supportsToolContract(provider, undefined, directory)
        )
        if (unknown) {
          ctx.addIssue({
            code: 'custom',
            message: `${t('Unknown provider')}: ${unknown}`,
          })
        } else if (new Set(providers).size !== providers.length) {
          ctx.addIssue({
            code: 'custom',
            message: t('Provider order cannot contain duplicates'),
          })
        }
      }),
      fallback_enabled: z.boolean().optional(),
      local_fetch_max_bytes: z.number().int().min(1).max(8 * 1024 * 1024).optional(),
      native_first: z.boolean(),
      builtin_fallback_enabled: z.boolean(),
      search_cache_ttl_seconds: z.number().int().min(-1),
      fetch_cache_ttl_seconds: z.number().int().min(-1),
      redirect_cache_ttl_seconds: z.number().int().min(-1),
      response_header_timeout_seconds: z.number().int().min(0),
      retry_attempts: z.number().int().min(0).max(5),
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
  'websearch_setting.builtin_fallback_enabled': boolean
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
    builtin_fallback_enabled:
      defaults['websearch_setting.builtin_fallback_enabled'] ?? false,
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
  'websearch_setting.builtin_fallback_enabled':
    defaults['websearch_setting.builtin_fallback_enabled'] ?? false,
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
  'websearch_setting.builtin_fallback_enabled':
    values.websearch_setting.builtin_fallback_enabled,
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
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()
  const [clearKeyOpen, setClearKeyOpen] = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<'default' | 'policies'>(() =>
    typeof window !== 'undefined' && window.location.hash === '#group-policies'
      ? 'policies'
      : 'default'
  )

  useEffect(() => {
    const syncHash = () => {
      if (window.location.hash === '#group-policies') {
        setActiveTab('policies')
      }
    }
    window.addEventListener('hashchange', syncHash)
    return () => window.removeEventListener('hashchange', syncHash)
  }, [])
  const orderTitleId = useId()
  const cacheTitleId = useId()
  const upstreamTitleId = useId()

  const baselineRef = useRef<FlatToolsProxySettings>(
    normalizeDefaults(defaultValues)
  )
  const baselineSerializedRef = useRef<string>(
    JSON.stringify(normalizeDefaults(defaultValues))
  )

  const options = useSystemOptions().data?.data
  const searchFallback = options?.find(({ key }) => key === 'websearch_setting.fallback_enabled')?.value
  const fetchLimit = options?.find(({ key }) => key === 'websearch_setting.local_fetch_max_bytes')?.value
  const formDefaults = useMemo(() => {
    const defaults = buildFormDefaults(defaultValues)
    defaults.websearch_setting.fallback_enabled = searchFallback === undefined ? undefined : searchFallback === 'true'
    defaults.websearch_setting.local_fetch_max_bytes = fetchLimit === undefined ? undefined : Number(fetchLimit)
    return defaults
  }, [defaultValues, searchFallback, fetchLimit])

  const directoryQuery = useQuery({
    queryKey: summaryKey,
    queryFn: getProviderSummary,
    meta: { errorToast: false },
  })
  const directory = directoryQuery.isError ? undefined : directoryQuery.data
  const toolsProxySchema = useMemo(() => createToolsProxySchema(t, directory), [t, directory])

  const form = useForm<ToolsProxyFormValues>({
    resolver: zodResolver(toolsProxySchema),
    defaultValues: formDefaults,
  })

  const { isDirty, isSubmitting } = form.formState
  useResetForm(form, isDirty || isSubmitting ? undefined : formDefaults)

  useEffect(() => {
    const normalized = normalizeDefaults(defaultValues)
    const serialized = JSON.stringify(normalized)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = normalized
    baselineSerializedRef.current = serialized
  }, [defaultValues])

  const exaKeyConfigured = defaultValues.ExaKeyConfigured === true
  const refreshProviderReadiness = () => {
    void queryClient.invalidateQueries({ queryKey: summaryKey })
  }

  const clearExaKey = async () => {
    if (!exaKeyConfigured || updateOption.isPending || isSubmitting) return

    try {
      await updateOption.mutateAsync({
        key: 'websearch_setting.exa_api_key',
        value: '',
      })
    } catch {
      return
    }

    form.resetField('websearch_setting.exa_api_key')
    refreshProviderReadiness()
    setClearKeyOpen(false)
  }

  const onSubmit = async (values: ToolsProxyFormValues) => {
    if (clearKeyOpen) return

    const normalized = normalizeFormValues(values)
    const fallback = values.websearch_setting.fallback_enabled
    const maxBytes = values.websearch_setting.local_fetch_max_bytes
    const saveFallback = searchFallback !== undefined && fallback !== undefined && fallback !== (searchFallback === 'true')
    const saveLimit = fetchLimit !== undefined && maxBytes !== undefined && maxBytes !== Number(fetchLimit)
    const updates = (
      Object.keys(normalized) as Array<keyof FlatToolsProxySettings>
    ).filter((key) => {
      if (key === 'websearch_setting.retry_attempts' || key === 'websearch_setting.retry_base_delay_milliseconds') return false
      if (normalized[key] === baselineRef.current[key]) return false
      // An empty key field means "leave the stored key alone".
      if (key === 'websearch_setting.exa_api_key' && normalized[key] === '') {
        return false
      }
      return true
    })

    if (!updates.length && !saveFallback && !saveLimit) {
      toast.info(t('No changes to save'))
      return
    }

    if (saveFallback) {
      await updateOption.mutateAsync({ key: 'websearch_setting.fallback_enabled', value: fallback })
    }
    if (saveLimit) {
      await updateOption.mutateAsync({ key: 'websearch_setting.local_fetch_max_bytes', value: maxBytes })
    }

    for (const key of updates) {
      await updateOption.mutateAsync({
        key,
        value: normalized[key],
      })
      if (key === 'websearch_setting.exa_api_key') refreshProviderReadiness()
    }

    // The key field is write-only, so the baseline never holds a key: clearing
    // it here keeps a second entry of the same value from looking unchanged.
    const nextBaseline: FlatToolsProxySettings = {
      ...normalized,
      'websearch_setting.exa_api_key': '',
    }
    baselineRef.current = nextBaseline
    baselineSerializedRef.current = JSON.stringify(nextBaseline)
    form.reset({
      websearch_setting: { ...values.websearch_setting, exa_api_key: '' },
    })
  }

  return (
    <SettingsSection title={t('Tools Proxy')} className='gap-5'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <ToolsNavigation />
        <Sheet>
          <SheetTrigger
            render={<Button type='button' variant='outline' size='sm' />}
          >
            <Database className='mr-1.5 size-3.5' aria-hidden='true' />
            {t('Tool cache')}
          </SheetTrigger>
          <SheetContent className='overflow-y-auto sm:max-w-lg'>
            <SheetHeader>
              <SheetTitle>{t('Tool cache')}</SheetTitle>
              <SheetDescription>
                {t('Inspect or invalidate cached search and fetch answers.')}
              </SheetDescription>
            </SheetHeader>
            <div className='px-4 pb-4'>
              <ToolsCache />
            </div>
          </SheetContent>
        </Sheet>
      </div>
      <ToolsDiagnostics />
      <Tabs
        value={activeTab}
        onValueChange={(value) => setActiveTab(value as 'default' | 'policies')}
        className='gap-4'
      >
        <TabsList>
          <TabsTrigger value='default'>{t('Global defaults')}</TabsTrigger>
          <TabsTrigger value='policies'>{t('Group policies')}</TabsTrigger>
        </TabsList>

        <TabsContent value='default' className='mt-0'>
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
            {directoryQuery.isError && (
              <div className='flex flex-wrap items-center gap-2'>
                <p role='status' className='text-muted-foreground text-sm'>
                  {t('Provider directory is unavailable. Existing selections are preserved.')}
                </p>
                <Button type='button' variant='outline' size='sm' disabled={directoryQuery.isFetching} onClick={() => void directoryQuery.refetch()}>
                  {t('Retry')}
                </Button>
              </div>
            )}
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='websearch_setting.candidate_order'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Provider order')}</FormLabel>
                    <FormControl>
                      <ProviderOrderEditor
                        value={field.value}
                        directory={directory}
                        onChange={field.onChange}
                        onBlur={field.onBlur}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Leave the list empty to use the built-in order.')}
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
              <FormField
                control={form.control}
                name='websearch_setting.builtin_fallback_enabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>
                        {t('Enable built-in tool fallback')}
                      </FormLabel>
                      <FormDescription>
                        {t(
                          'Use configured Exa or local fetch when no eligible channel is available. Group policies still apply.'
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
            </SettingsFormGrid>

            <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
              <div className='rounded-lg border px-3.5 py-2.5'>
                <CollapsibleTrigger
                  render={
                    <button
                      type='button'
                      className='flex w-full items-center justify-between gap-2 py-1 text-left text-sm font-medium'
                    />
                  }
                >
                  <span>{t('Advanced network & legacy settings')}</span>
                  <ChevronDown
                    className={cn(
                      'text-muted-foreground size-4 transition-transform',
                      advancedOpen && 'rotate-180'
                    )}
                    aria-hidden='true'
                  />
                </CollapsibleTrigger>
                <CollapsibleContent className='pt-3'>
                  <SettingsFormGrid>
                    {searchFallback !== undefined && (
                      <FormField control={form.control} name='websearch_setting.fallback_enabled' render={({ field }) => (
                        <SettingsSwitchItem>
                          <SettingsSwitchContent><FormLabel>{t('Inject default search parameters')}</FormLabel></SettingsSwitchContent>
                          <FormControl><Switch checked={field.value ?? false} onCheckedChange={field.onChange} /></FormControl>
                        </SettingsSwitchItem>
                      )} />
                    )}
                    {fetchLimit !== undefined && (
                      <FormField control={form.control} name='websearch_setting.local_fetch_max_bytes' render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Local fetch limit (bytes)')}</FormLabel>
                          <FormControl><Input type='number' min={1} max={8 * 1024 * 1024} step={1} {...safeNumberFieldProps(field)} /></FormControl>
                          <FormMessage />
                        </FormItem>
                      )} />
                    )}
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
                              max={5}
                              readOnly
                              step={1}
                              {...safeNumberFieldProps(field)}
                            />
                          </FormControl>
                          <FormDescription>
                            {t('Legacy setting. Tool requests are not retried after sending.')}
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
                              readOnly
                              min={0}
                              step={1}
                              {...safeNumberFieldProps(field)}
                            />
                          </FormControl>
                          <FormDescription>
                            {t('Legacy setting. Tool requests are not retried after sending.')}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </SettingsFormGrid>
                </CollapsibleContent>
              </div>
            </Collapsible>
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
                    {exaKeyConfigured && (
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        className='justify-self-start'
                        disabled={isSubmitting || updateOption.isPending}
                        onClick={() => setClearKeyOpen(true)}
                      >
                        {t('Clear Exa key')}
                      </Button>
                    )}
                  </FormItem>
                )}
              />
              <div className='flex flex-col gap-2'>
                <span className='text-sm font-medium leading-none'>
                  {t('Antigravity search model')}
                </span>
                <div className='bg-muted/30 text-muted-foreground flex h-9 items-center rounded-md border px-3 font-mono text-sm'>
                  gemini-3.5-flash-lite
                </div>
              </div>
            </SettingsFormGrid>
          </div>
        </SettingsForm>
      </Form>
      <ConfirmDialog
        open={clearKeyOpen}
        onOpenChange={(open) => {
          if (!updateOption.isPending) setClearKeyOpen(open)
        }}
        title={t('Clear Exa key?')}
        desc={t('Tools Proxy will stop using the stored Exa key.')}
        confirmText={t('Clear')}
        destructive
        isLoading={updateOption.isPending}
        disabled={!exaKeyConfigured || isSubmitting}
        handleConfirm={() => void clearExaKey()}
      />
        </TabsContent>

        <TabsContent value='policies' className='mt-0'>
          <div id='group-policies' className='min-w-0 scroll-mt-16'>
            <ToolsApiContent />
          </div>
        </TabsContent>
      </Tabs>
    </SettingsSection>
  )
}
