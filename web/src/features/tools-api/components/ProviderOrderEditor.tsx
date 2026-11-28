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
  const isPinned = (provider: string) =>
    props.contract?.endsWith('/fetch') && provider === 'local'

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
        <ol className='flex flex-col gap-1'>
          {providers.map((provider, index) => {
            const supply = props.directory?.find((entry) => entry.provider === provider)
            const channelCount = props.contract
              ? supply?.contract_details?.find((detail) => detail.contract === props.contract)?.available_channels
              : supply?.available_channels
            return (
            <li
              key={`${provider}:${providers.slice(0, index).filter((entry) => entry === provider).length}`}
              className='flex flex-wrap items-center gap-1'
            >
              <div className='me-auto flex flex-wrap items-center gap-1'>
                <code className='wrap-anywhere'>{provider}</code>
                {props.contract && supply?.native_contracts?.includes(props.contract) && (
                  <Badge variant='outline'>{t('Native')}</Badge>
                )}
                {channelCount === 0 && !supply?.global_credential_configured && provider !== 'local' && (
                  <Badge variant='secondary'>{t('No configured channels')}</Badge>
                )}
              </div>
              <Button
                type='button'
                variant='outline'
                size='sm'
                aria-label={`${t('Move up')}: ${provider}`}
                disabled={props.disabled}
                aria-disabled={
                  index === 0 ||
                  !!isPinned(provider) ||
                  !!isPinned(providers[index - 1])
                }
                onClick={(event) => {
                  move(index, -1)
                  event.currentTarget.focus()
                }}
              >
                {t('Move up')}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                aria-label={`${t('Move down')}: ${provider}`}
                disabled={props.disabled}
                aria-disabled={
                  index === providers.length - 1 ||
                  !!isPinned(provider) ||
                  !!isPinned(providers[index + 1])
                }
                onClick={(event) => {
                  move(index, 1)
                  event.currentTarget.focus()
                }}
              >
                {t('Move down')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                aria-label={`${t('Remove')}: ${provider}`}
                disabled={props.disabled}
                onClick={() =>
                  update(providers.filter((_, position) => position !== index))
                }
              >
                {t('Remove')}
              </Button>
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
