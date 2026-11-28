import { ArrowDown, ArrowUp, X } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import {
  parseProviderOrder,
  supportsToolContract,
  TOOL_PROVIDERS,
  normalizeProviderOrder,
  type ToolContract,
} from '../lib/group-policies'
import type { ProviderSummary } from '../lib/admin'

type ProviderOrderEditorProps = {
  id?: string
  value: string
  onChange: (value: string) => void
  onBlur?: () => void
  ref?: React.Ref<HTMLSelectElement>
  'aria-labelledby'?: string
  contract?: ToolContract
  directory?: ProviderSummary[]
  disabled?: boolean
  'aria-describedby'?: string
  'aria-invalid'?: boolean
}

export function ProviderOrderEditor(props: ProviderOrderEditorProps) {
  const { t } = useTranslation()
  const [announcement, setAnnouncement] = useState('')
  const id = useId()
  const providers = normalizeProviderOrder(parseProviderOrder(props.value), props.contract)
  const available = (props.directory?.map((entry) => entry.provider) ?? TOOL_PROVIDERS).filter(
    (provider) =>
      !providers.includes(provider) &&
      supportsToolContract(provider, props.contract, props.directory) &&
      !(!props.directory && props.contract?.endsWith('/fetch') &&
        (provider === 'antigravity' || provider === 'codex'))
  )
  const isPinned = (_provider: string) => false

  function update(next: string[]) {
    props.onChange(normalizeProviderOrder(next, props.contract).join(', '))
  }

  function move(index: number, offset: number) {
    const destination = index + offset
    if (
      destination < 0 ||
      destination >= providers.length ||
      isPinned(providers[index]) ||
      isPinned(providers[destination])
    ) {
      return
    }
    const next = [...providers]
    next[destination] = providers[index]
    next[index] = providers[destination]
    update(next)
    setAnnouncement(next.join(' → '))
  }

  return (
    <div className='flex min-w-0 flex-col gap-2'>
      <NativeSelect
        id={props.id ?? id}
        ref={props.ref}
        aria-label={
          props['aria-labelledby'] || props.id ? undefined : t('Add provider')
        }
        aria-labelledby={props['aria-labelledby']}
        aria-describedby={props['aria-describedby']}
        aria-invalid={props['aria-invalid']}
        value=''
        disabled={props.disabled || !available.length}
        onBlur={props.onBlur}
        onChange={(event) => {
          if (available.includes(event.target.value)) {
            update([...providers, event.target.value])
          }
        }}
      >
        <NativeSelectOption value=''>{t('Add provider')}</NativeSelectOption>
        {available.map((provider) => (
          <NativeSelectOption key={provider} value={provider}>
            {provider}
          </NativeSelectOption>
        ))}
      </NativeSelect>
      {providers.length > 0 && (
        <ol className='flex flex-col gap-1.5'>
          {providers.map((provider, index) => {
            const supply = props.directory?.find((entry) => entry.provider === provider)
            const channelCount = props.contract
              ? supply?.contract_details?.find((detail) => detail.contract === props.contract)?.available_channels
              : supply?.available_channels
            const moveUpDisabled =
              index === 0 ||
              !!isPinned(provider) ||
              !!isPinned(providers[index - 1])
            const moveDownDisabled =
              index === providers.length - 1 ||
              !!isPinned(provider) ||
              !!isPinned(providers[index + 1])
            return (
              <li
                key={`${provider}:${providers.slice(0, index).filter((entry) => entry === provider).length}`}
                className='bg-muted/30 flex items-center gap-2 rounded-md border px-2.5 py-1.5'
              >
                <span className='bg-background text-muted-foreground inline-flex size-5 shrink-0 items-center justify-center rounded-full border font-mono text-[11px] tabular-nums'>
                  {index + 1}
                </span>
                <div className='me-auto flex min-w-0 flex-wrap items-center gap-1.5'>
                  <code className='text-sm font-medium wrap-anywhere'>{provider}</code>
                  {props.contract && supply?.native_contracts?.includes(props.contract) && (
                    <Badge variant='outline' className='h-5 text-[11px]'>
                      {t('Native')}
                    </Badge>
                  )}
                  {channelCount === 0 && !supply?.global_credential_configured && provider !== 'local' && (
                    <Badge variant='secondary' className='h-5 text-[11px]'>
                      {t('No configured channels')}
                    </Badge>
                  )}
                </div>
                <div className='flex shrink-0 items-center gap-1'>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-xs'
                    title={t('Move up')}
                    aria-label={`${t('Move up')}: ${provider}`}
                    disabled={props.disabled}
                    aria-disabled={moveUpDisabled}
                    onClick={(event) => {
                      move(index, -1)
                      event.currentTarget.focus()
                    }}
                  >
                    <ArrowUp className='size-3.5' aria-hidden='true' />
                  </Button>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-xs'
                    title={t('Move down')}
                    aria-label={`${t('Move down')}: ${provider}`}
                    disabled={props.disabled}
                    aria-disabled={moveDownDisabled}
                    onClick={(event) => {
                      move(index, 1)
                      event.currentTarget.focus()
                    }}
                  >
                    <ArrowDown className='size-3.5' aria-hidden='true' />
                  </Button>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-xs'
                    title={t('Remove')}
                    aria-label={`${t('Remove')}: ${provider}`}
                    disabled={props.disabled}
                    className='text-muted-foreground hover:text-destructive'
                    onClick={() =>
                      update(providers.filter((_, position) => position !== index))
                    }
                  >
                    <X className='size-3.5' aria-hidden='true' />
                  </Button>
                </div>
              </li>
            )
          })}
        </ol>
      )}
      <span role='status' className='sr-only'>
        {announcement}
      </span>
    </div>
  )
}
