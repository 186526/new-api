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
import { useFormContext } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

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

import type { ChannelFormValues } from '../lib/channel-form'
import { supportsWebSearchSettings } from '../lib/websearch-setting'

/** The select uses a sentinel because an empty value means "derive". */
const PROVIDER_DERIVED = 'auto'

export function WebSearchSetting(props: {
  channelType: number
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  if (!supportsWebSearchSettings(props.channelType)) return null

  const providerItems = [
    { value: PROVIDER_DERIVED, label: t('From channel type') },
    { value: 'antigravity', label: 'Antigravity' },
    { value: 'deepseek', label: 'DeepSeek' },
    { value: 'exa', label: 'Exa' },
    { value: 'local', label: t('Local fetch') },
  ]

  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Applies to /moonshot.service/v1 on this channel. /v1/tools/* and /v1/alpha/search keep forwarding the upstream answer unchanged.'
        )}
      </p>

      <FormField
        control={form.control}
        name='websearch_provider'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Search and fetch provider')}</FormLabel>
            <Select
              disabled={props.disabled}
              items={providerItems}
              value={field.value ?? PROVIDER_DERIVED}
              onValueChange={(value) => {
                field.onChange(value === PROVIDER_DERIVED ? undefined : value)
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
              {t(
                'Antigravity channels search through Antigravity, DeepSeek channels through DeepSeek. Leave it on the channel type unless this channel should use another provider.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name='antigravity_web_search_model'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Antigravity search model')}</FormLabel>
            <FormControl>
              <Input
                placeholder='gemini-3.5-flash-lite'
                disabled={props.disabled}
                value={field.value ?? ''}
                onChange={field.onChange}
                onBlur={field.onBlur}
                name={field.name}
                ref={field.ref}
              />
            </FormControl>
            <FormDescription>
              {t(
                'Model the Antigravity search is sent to. Leave empty to use gemini-3.5-flash-lite.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

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
    </div>
  )
}
