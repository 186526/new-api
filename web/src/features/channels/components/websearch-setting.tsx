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
import { useId } from 'react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import {
  CHANNEL_TYPE_ADVANCED_CUSTOM,
  CHANNEL_TYPE_CODEX,
  TOOLS_FETCH_MODEL,
  TOOLS_SEARCH_MODEL,
} from '../constants'
import type { ChannelFormValues } from '../lib/channel-form'
import {
  formatModelsArray,
  parseModelsString,
} from '../lib/model-mapping-validation'
import {
  getChannelToolAvailability,
  toolModelMapping,
  withToolModelMapping,
  type ChannelToolCatalog,
  type ToolModel,
} from '../lib/websearch-setting'

function ToolSwitch(props: {
  label: string
  checked: boolean
  disabled?: boolean
  onChange: (checked: boolean) => void
}) {
  const id = useId()
  return (
    <Field orientation='horizontal' data-disabled={props.disabled}>
      <Switch
        id={id}
        nativeButton
        render={<button type='button' />}
        checked={props.checked}
        disabled={props.disabled}
        onCheckedChange={props.onChange}
      />
      <FieldLabel htmlFor={id}>{props.label}</FieldLabel>
    </Field>
  )
}

/** The select uses a sentinel because an empty value means "derive". */
const PROVIDER_DERIVED = 'auto'
const PROVIDER_NAMES: Record<string, string> = {
  antigravity: 'Antigravity',
  codex: 'Codex',
  deepseek: 'DeepSeek',
  exa: 'Exa',
  moonshot: 'Moonshot',
  perplexity: 'Perplexity',
}

function ToolModelMappingField(props: {
  model: ToolModel
  label: string
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  return (
    <FormField
      control={form.control}
      name='model_mapping'
      render={({ field }) => {
        const target = toolModelMapping(field.value ?? '', props.model)
        return (
          <FormItem>
            <FormLabel>{props.label}</FormLabel>
            <FormControl>
              <Input
                name={props.model}
                value={target ?? ''}
                onChange={(event) => {
                  const mapping = withToolModelMapping(
                    form.getValues('model_mapping') ?? '',
                    props.model,
                    event.currentTarget.value
                  )
                  if (mapping !== undefined) field.onChange(mapping)
                }}
                onBlur={field.onBlur}
                disabled={props.disabled || target === undefined}
                aria-required='true'
                autoCapitalize='none'
                spellCheck={false}
              />
            </FormControl>
            <FormDescription>
              {target === undefined && t('Fix the model mapping JSON in Routing & Mapping before editing this field.')}
              {target !== undefined && props.model === TOOLS_SEARCH_MODEL && t('Maps tools-search to the upstream model.')}
              {target !== undefined && props.model === TOOLS_FETCH_MODEL && t('Maps tools-fetch to the upstream model.')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )
      }}
    />
  )
}

export function WebSearchSetting(props: {
  channelType: number
  toolCatalog?: ChannelToolCatalog
  disabled?: boolean
  settingsLocked?: boolean
}) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  const models = parseModelsString(
    useWatch({ control: form.control, name: 'models' })
  )
  const provider = useWatch({
    control: form.control,
    name: 'websearch_provider',
  })
  const availability = getChannelToolAvailability(
    props.channelType,
    props.toolCatalog,
    provider
  )
  const channelProvider = availability.provider
  const preferredProvider = provider && provider !== 'local' ? provider : channelProvider?.provider
  const antigravity = preferredProvider === 'antigravity'
  const preferredMetadata = Object.values(props.toolCatalog?.channel_types ?? {}).find(
    (item) => item.provider === preferredProvider
  )
  const fixedSearchModel = preferredMetadata?.search.fixed_model ??
    (antigravity ? 'gemini-3.5-flash-lite' : undefined)
  const searchEnabled = models.includes(TOOLS_SEARCH_MODEL)
  const fetchEnabled = models.includes(TOOLS_FETCH_MODEL)
  if (
    props.toolCatalog &&
    !availability.canConfigure &&
    !searchEnabled &&
    !fetchEnabled
  ) {
    return null
  }
  const providerName =
    channelProvider &&
    (PROVIDER_NAMES[channelProvider.provider] ?? channelProvider.provider)
  const providerItems = [
    { value: PROVIDER_DERIVED, label: t('From channel type') },
    { value: 'antigravity', label: 'Antigravity' },
    { value: 'deepseek', label: 'DeepSeek' },
    ...(props.channelType !== CHANNEL_TYPE_ADVANCED_CUSTOM || provider === 'exa'
      ? [{ value: 'exa', label: 'Exa' }]
      : []),
  ]

  function toggle(model: ToolModel, enabled: boolean) {
    const selected = parseModelsString(form.getValues('models'))
    form.setValue(
      'models',
      formatModelsArray(
        enabled
          ? [...selected, model]
          : selected.filter((item) => item !== model)
      ),
      { shouldDirty: true, shouldValidate: form.formState.isSubmitted }
    )
  }

  return (
    <div className='flex flex-col gap-4'>
      <ToolSwitch
        label={t('Enable search middleware')}
        checked={searchEnabled}
        disabled={props.disabled || (!availability.canSearch && !searchEnabled)}
        onChange={(enabled) => toggle(TOOLS_SEARCH_MODEL, enabled)}
      />
      <ToolSwitch
        label={t('Enable web fetch')}
        checked={fetchEnabled}
        disabled={props.disabled || (!availability.canFetch && !fetchEnabled)}
        onChange={(enabled) => toggle(TOOLS_FETCH_MODEL, enabled)}
      />
      {availability.canFetch && searchEnabled && !fetchEnabled && (
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='w-fit'
          disabled={props.disabled}
          onClick={() => toggle(TOOLS_FETCH_MODEL, true)}
        >
          {t('Enable fetch for Kimi Code')}
        </Button>
      )}
      {(searchEnabled || fetchEnabled) && (
        <>
          {channelProvider?.search.supported && (
            <p className='text-muted-foreground text-xs'>
              {channelProvider.fetch.supported
                ? t('Native search and fetch: {{provider}}', {
                    provider: providerName,
                  })
                : availability.localFetch &&
                  t(
                    'Native search: {{provider}}. Fetch can use the built-in fetcher.',
                    { provider: providerName }
                  )}
            </p>
          )}
          {availability.requiresSearchMapping && searchEnabled && (
            <ToolModelMappingField
              model={TOOLS_SEARCH_MODEL}
              label={props.channelType === CHANNEL_TYPE_CODEX
                ? t('Codex upstream search model')
                : t('Upstream search model')}
              disabled={props.disabled}
            />
          )}
          {availability.requiresFetchMapping && fetchEnabled && (
            <ToolModelMappingField
              model={TOOLS_FETCH_MODEL}
              label={t('Upstream fetch model')}
              disabled={props.disabled}
            />
          )}
          <fieldset
            disabled={props.disabled || props.settingsLocked}
            className='space-y-4 disabled:opacity-60'
          >
            {(availability.canConfigure ||
              (!props.toolCatalog && provider)) && (
              <FormField
                control={form.control}
                name='websearch_provider'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Search and fetch provider')}</FormLabel>
                    <Select
                      disabled={props.disabled || props.settingsLocked}
                      items={providerItems}
                      value={
                        field.value === 'local'
                          ? PROVIDER_DERIVED
                          : (field.value ?? PROVIDER_DERIVED)
                      }
                      onValueChange={(value) => {
                        field.onChange(
                          value === PROVIDER_DERIVED ? undefined : value
                        )
                      }}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {providerItems.map((item) => (
                            <SelectItem key={item.value} value={item.value}>
                              {item.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {field.value === 'local'
                        ? t(
                            'Legacy Local preference is ignored. Built-in fetch remains a fallback.'
                          )
                        : t(
                            'Prefer this provider when the group policy allows it.'
                          )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            )}

            {fixedSearchModel && searchEnabled && (
              <dl>
                <dt>{antigravity ? t('Antigravity search model') : t('Search model')}</dt>
                <dd className='text-muted-foreground text-sm'>
                  {fixedSearchModel}
                </dd>
              </dl>
            )}

            {searchEnabled && (
              <FormField
                control={form.control}
                name='websearch_cache_ttl_seconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Search cache TTL (seconds)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={-1}
                        placeholder={t('Use the default')}
                        disabled={props.disabled}
                        value={field.value ?? ''}
                        onChange={(event) => {
                          const raw = event.target.value
                          field.onChange(raw === '' ? undefined : Number(raw))
                        }}
                        onBlur={field.onBlur}
                        name={field.name}
                        ref={field.ref}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'How long a search answer is reused. Leave empty for the default, enter 0 to keep the default, or -1 to disable the cache.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            )}

            {fetchEnabled && (
              <FormField
                control={form.control}
                name='websearch_fetch_cache_ttl_seconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Fetch cache TTL (seconds)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={-1}
                        placeholder={t('Use the default')}
                        disabled={props.disabled}
                        value={field.value ?? ''}
                        onChange={(event) => {
                          const raw = event.target.value
                          field.onChange(raw === '' ? undefined : Number(raw))
                        }}
                        onBlur={field.onBlur}
                        name={field.name}
                        ref={field.ref}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'How long a fetched page is reused. Leave empty for the default, enter 0 to keep the default, or -1 to disable the cache.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            )}
          </fieldset>
        </>
      )}
    </div>
  )
}
