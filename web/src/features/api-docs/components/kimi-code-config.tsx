import { useQuery } from '@tanstack/react-query'
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { fetchTokenKey, getApiKeys } from '@/features/keys/api'
import { API_KEY_STATUS } from '@/features/keys/constants'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { buildKimiCodeConfig } from '../lib/kimi-code'
import { DocsCode } from './docs-code'

type KeyOption = { id: number; name: string; group: string }

type CopyStatus = 'loading' | 'copied' | 'key-error' | 'copy-error'

async function listEnabledKeys(signal: AbortSignal): Promise<KeyOption[]> {
  const options: KeyOption[] = []
  for (let page = 1; !signal.aborted; page += 1) {
    const result = requireServerSuccess(
      await getApiKeys({ p: page, size: 100 })
    )
    if (!result.data?.items?.length) break
    const { items, page_size, total } = result.data
    for (const key of items) {
      if (key.status === API_KEY_STATUS.ENABLED) {
        options.push({ id: key.id, name: key.name, group: key.group ?? '' })
      }
    }
    if (page * page_size >= total || page_size <= 0) break
  }
  return options
}

function KeyConfigCopy(props: { baseUrl: string; userId: number; initialGroup?: string }) {
  const { t } = useTranslation()
  const selectId = useId()
  const [group, setGroup] = useState(props.initialGroup ?? '')
  const [selection, setSelection] = useState<string | null>(null)
  const [status, setStatus] = useState<CopyStatus>()
  const inFlight = useRef(false)
  const query = useQuery({
    queryKey: ['docs', 'kimi-code-key-options', props.userId],
    queryFn: ({ signal }) => listEnabledKeys(signal),
    retry: false,
    meta: { errorToast: false },
  })
  const groups = [...new Set([group, ...(query.data ?? []).map((key) => key.group)])].filter(Boolean)
  const options = (query.data ?? [])
    .filter((key) => !group || key.group === group)
    .map((key) => ({
      value: String(key.id),
      label: `${key.name} · ${key.group || t('Inherit user group')}`,
    }))
  const selected = options.find((key) => key.value === selection)

  async function copySelectedKey(): Promise<void> {
    if (!selected || inFlight.current) return
    inFlight.current = true
    setStatus('loading')
    try {
      const result = await fetchTokenKey(Number(selected.value)).catch(
        () => null
      )
      if (useAuthStore.getState().auth.user?.id !== props.userId) return
      if (!result?.success || !result.data?.key) {
        setStatus('key-error')
        return
      }
      const config = buildKimiCodeConfig(props.baseUrl, `sk-${result.data.key}`)
      const copied = await copyToClipboard(config).catch(() => false)
      setStatus(copied ? 'copied' : 'copy-error')
    } finally {
      inFlight.current = false
    }
  }

  if (query.isPending) return <p role='status'>{t('Loading API keys...')}</p>
  if (query.isError) {
    return (
      <div className='flex flex-wrap items-center gap-2'>
        <p role='status'>{t('Failed to load API keys')}</p>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void query.refetch()}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  }
  if (!query.data?.length) {
    return <p>{t('No enabled API keys found. Create or enable one first.')}</p>
  }

  return (
    <div className='flex flex-col gap-2'>
      <Field>
        <FieldLabel htmlFor={`${selectId}-group`}>{t('Filter keys by group')}</FieldLabel>
        <NativeSelect
          id={`${selectId}-group`}
          value={group}
          disabled={status === 'loading'}
          onChange={(event) => {
            setGroup(event.target.value)
            setSelection(null)
            setStatus(undefined)
          }}
        >
          <NativeSelectOption value=''>{t('All groups')}</NativeSelectOption>
          {groups.map((name) => (
            <NativeSelectOption key={name} value={name}>{name}</NativeSelectOption>
          ))}
        </NativeSelect>
      </Field>
      {!options.length && <p role='status'>{t('No enabled API keys match this group filter.')}</p>}
      <Field>
        <FieldLabel htmlFor={selectId}>{t('API Key')}</FieldLabel>
        <div className='flex flex-wrap items-center gap-2'>
          <Select
            items={options}
            value={selected?.value ?? null}
            disabled={status === 'loading'}
            onValueChange={(value) => {
              setSelection(value)
              setStatus(undefined)
            }}
          >
            <SelectTrigger id={selectId} className='w-full min-w-0 sm:w-72'>
              <SelectValue placeholder={t('Select an API key')} />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {options.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={!selected}
            aria-busy={status === 'loading'}
            onClick={() => void copySelectedKey()}
          >
            {t('apiDocs.kimiCode.copyWithKey', {
              defaultValue: 'Copy with key',
            })}
          </Button>
        </div>
      </Field>
      <p className='text-muted-foreground text-sm'>
        {t('apiDocs.kimiCode.keyNotice', {
          defaultValue:
            'Copy with key includes the full API key. Keep the config private.',
        })}
      </p>
      <p className='text-muted-foreground text-sm' role='status'>
        {status === 'loading' &&
          t('apiDocs.kimiCode.copying', { defaultValue: 'Copying…' })}
        {status === 'copied' && t('Copied to clipboard')}
        {status === 'key-error' && t('Failed to load API keys')}
        {status === 'copy-error' && t('Failed to copy to clipboard')}
      </p>
    </div>
  )
}

export function KimiCodeConfig(props: { baseUrl: string; initialGroup?: string }) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)

  return (
    <div className='flex min-w-0 flex-col gap-3'>
      <h3 className='text-sm font-medium'>Kimi Code</h3>
      <p className='text-muted-foreground text-sm'>
        {t('apiDocs.kimiCode.instructions', {
          defaultValue:
            'Merge these sections into config.toml. Use a key with access to tools-search and tools-fetch.',
        })}
      </p>
      <DocsCode
        code={buildKimiCodeConfig(props.baseUrl)}
        title='config.toml'
        copyLabel={t('apiDocs.kimiCode.copyTemplate', {
          defaultValue: 'Copy Kimi Code template',
        })}
      />
      {userId != null && (
        <KeyConfigCopy
          key={`${userId}:${props.initialGroup ?? ''}`}
          baseUrl={props.baseUrl}
          userId={userId}
          initialGroup={props.initialGroup}
        />
      )}
    </div>
  )
}
