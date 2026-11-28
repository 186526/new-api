import { useQuery } from '@tanstack/react-query'
import { useFormContext } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  Combobox,
  ComboboxChip,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxCollection,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  ComboboxValue,
  useComboboxAnchor,
} from '@/components/ui/combobox'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { getChannels } from '@/features/channels/api'
import { getChannelTypeLabel } from '@/features/channels/lib/channel-utils'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  splitCompressionScope,
  type ImageCompressionSettings,
} from './image-compression-settings'

async function loadChannelDirectory(signal: AbortSignal) {
  const directory = new Map<string, string>()
  for (let page = 1; !signal.aborted; page++) {
    const { data } = requireServerSuccess(
      await getChannels({ p: page, page_size: 100, id_sort: true })
    )
    for (const channel of data?.items ?? []) {
      directory.set(
        String(channel.id),
        `${channel.name} · ${getChannelTypeLabel(channel.type)} (#${channel.id})`
      )
    }
    if (!data?.items.length || page * data.page_size >= data.total) break
  }
  return directory
}

type ChannelPickerProps = {
  value: string
  onChange: (value: string) => void
  disabled: boolean
  directory?: Map<string, string>
}

function ChannelPicker(props: ChannelPickerProps) {
  const { t } = useTranslation()
  const anchor = useComboboxAnchor()
  const selected = [
    ...new Set(splitCompressionScope(props.value).map((id) => id.replace(/^\+?0*/, ''))),
  ]
  const items = [...new Set([...(props.directory?.keys() ?? []), ...selected])]
  const label = (id: string) => props.directory?.get(id) ?? `#${id}`

  return (
    <Combobox
      multiple
      items={items}
      value={selected}
      itemToStringLabel={label}
      onValueChange={(values: string[]) => props.onChange(values.join(', '))}
      disabled={props.disabled}
    >
      <ComboboxChips ref={anchor}>
        <ComboboxValue>
          {(values: string[]) => values.map((id) => (
            <ComboboxChip key={id}>{label(id)}</ComboboxChip>
          ))}
        </ComboboxValue>
        <FormControl>
          <ComboboxChipsInput
            onKeyDown={(event) => {
              if (event.key === 'Enter') event.preventDefault()
            }}
          />
        </FormControl>
      </ComboboxChips>
      <ComboboxContent anchor={anchor}>
        <ComboboxEmpty>{t('No channels found')}</ComboboxEmpty>
        <ComboboxList>
          <ComboboxCollection>
            {(id: string) => <ComboboxItem key={id} value={id}>{label(id)}</ComboboxItem>}
          </ComboboxCollection>
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}

export function ImageCompressionScope(props: { disabled: boolean }) {
  const { t } = useTranslation()
  const { control } = useFormContext<ImageCompressionSettings>()
  const directory = useQuery({
    queryKey: ['image-compression-channel-directory'],
    queryFn: ({ signal }) => loadChannelDirectory(signal),
    enabled: !props.disabled,
    staleTime: 60_000,
    meta: { errorToast: false },
  })
  return (
    <>
      {([
        ['channel_ids', t('Included channels'), t('All channels when empty.')],
        ['exclude_channel_ids', t('Excluded channels'), t('Excluded channels take precedence.')],
      ] as const).map(([name, label, description]) => (
        <FormField key={name} control={control} name={name} render={({ field }) => (
          <FormItem>
            <FormLabel>{label}</FormLabel>
            <ChannelPicker
              value={field.value}
              onChange={field.onChange}
              disabled={props.disabled || directory.isPending || directory.isError}
              directory={directory.data}
            />
            <FormDescription>
              {directory.isError ? t('Could not load channels.') : description}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )} />
      ))}
      {([
        ['model_patterns', t('Model globs (comma separated)'), t('All models when empty.')],
        ['exclude_model_patterns', t('Excluded model globs'), t('Excluded model globs take precedence.')],
      ] as const).map(([name, label, description]) => (
        <FormField key={name} control={control} name={name} render={({ field }) => (
          <FormItem>
            <FormLabel>{label}</FormLabel>
            <FormControl><Input {...field} disabled={props.disabled} /></FormControl>
            <FormDescription>{description}</FormDescription>
            <FormMessage />
          </FormItem>
        )} />
      ))}
    </>
  )
}
