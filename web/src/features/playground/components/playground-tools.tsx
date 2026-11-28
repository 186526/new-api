import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useEffect, useId, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  TOOL_CONTRACTS,
  TOOL_ENDPOINTS,
} from '@/features/tools-api/lib/group-policies'

import {
  listToolsKeys,
  runToolRequest,
  toolsFormSchema,
  type ToolResponse,
  type ToolsFormValues,
} from '../lib/tools-request'

export function PlaygroundTools(props: {
  userId: number
  endpoint: ToolsFormValues['endpoint']
  group: string
  model?: string
  fixedChannelId?: number
}) {
  const { t } = useTranslation()
  const id = useId()
  const pending = useRef<AbortController | null>(null)
  const [result, setResult] = useState<ToolResponse>()
  const [failed, setFailed] = useState(false)
  const [group, setGroup] = useState(props.group)
  const keys = useQuery({
    queryKey: ['playground', 'tool-keys', props.userId],
    queryFn: ({ signal }) => listToolsKeys(signal),
    retry: false,
    meta: { errorToast: false },
  })
  const form = useForm<ToolsFormValues>({
    resolver: zodResolver(toolsFormSchema),
    defaultValues: {
      endpoint: props.endpoint,
      keyId: '',
      input: '',
      model: props.model ?? '',
    },
  })
  const endpoint = form.watch('endpoint')
  const fetchEndpoint = endpoint.endsWith('/fetch')
  const allKeys = keys.data ?? []
  const groups = [...new Set(allKeys.map((key) => key.group).filter(Boolean))]
  const options = group ? allKeys.filter((key) => key.group === group) : allKeys
  const { errors, isSubmitting } = form.formState
  const inputError = fetchEndpoint
    ? t('Enter an HTTP or HTTPS URL without credentials.')
    : t('Enter a search query.')

  useEffect(() => () => pending.current?.abort(), [])

  async function submit(values: ToolsFormValues): Promise<void> {
    if (pending.current) return
    setResult(undefined)
    setFailed(false)
    if (!options.some((key) => String(key.id) === values.keyId)) {
      form.setError('keyId', { type: 'validate' }, { shouldFocus: true })
      return
    }
    const controller = new AbortController()
    pending.current = controller
    try {
      const response = await runToolRequest(values, controller.signal, props.fixedChannelId)
      if (!controller.signal.aborted) setResult(response)
    } catch {
      if (!controller.signal.aborted) setFailed(true)
    } finally {
      pending.current = null
    }
  }

  return (
    <div className='mx-auto flex w-full max-w-4xl flex-col gap-6 px-4 py-5 sm:px-6'>
      <form
        aria-labelledby={`${id}-title`}
        aria-busy={isSubmitting}
        method='post'
        noValidate
        onSubmit={(event) => {
          event.stopPropagation()
          void form.handleSubmit(submit)(event)
        }}
      >
        <FieldGroup>
          <div>
            <h2 id={`${id}-title`} className='font-semibold'>
              {t('Test Tools API')}
            </h2>
            <FieldDescription>
              {props.fixedChannelId === undefined
                ? t('Tool requests use your selected API key and may incur charges.')
                : t('Requests use saved channel #{{channelId}} and may incur charges.', {
                    channelId: props.fixedChannelId,
                  })}
            </FieldDescription>
          </div>
          <Field>
            <FieldLabel htmlFor={`${id}-endpoint`}>{t('Endpoint')}</FieldLabel>
            <NativeSelect
              id={`${id}-endpoint`}
              className='w-full'
              disabled={isSubmitting}
              {...form.register('endpoint')}
            >
              {TOOL_CONTRACTS.map((contract) => (
                <NativeSelectOption key={contract} value={contract}>
                  {TOOL_ENDPOINTS[contract]}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-group`}>
              {t('Filter keys by group')}
            </FieldLabel>
            <NativeSelect
              id={`${id}-group`}
              name='group'
              className='w-full'
              value={group}
              disabled={isSubmitting}
              onChange={(event) => {
                setGroup(event.currentTarget.value)
                form.resetField('keyId')
              }}
            >
              <NativeSelectOption value=''>
                {t('All groups')}
              </NativeSelectOption>
              {group && !groups.includes(group) && (
                <NativeSelectOption value={group}>{group}</NativeSelectOption>
              )}
              {groups.map((name) => (
                <NativeSelectOption key={name} value={name}>
                  {name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <Field data-invalid={!!errors.keyId}>
            <FieldLabel htmlFor={`${id}-key`}>{t('API Key')}</FieldLabel>
            <NativeSelect
              id={`${id}-key`}
              className='w-full'
              required
              disabled={keys.isPending || keys.isError || isSubmitting}
              aria-invalid={!!errors.keyId}
              aria-describedby={errors.keyId ? `${id}-key-error` : undefined}
              {...form.register('keyId')}
            >
              <NativeSelectOption value=''>
                {keys.isPending
                  ? t('Loading API keys...')
                  : t('Select an API key')}
              </NativeSelectOption>
              {options.map((key) => (
                <NativeSelectOption
                  key={key.id}
                  value={String(key.id)}
                >{`${key.name} · ${key.group || t('Inherit user group')}`}</NativeSelectOption>
              ))}
            </NativeSelect>
            {keys.isError && (
              <div className='flex flex-wrap items-center gap-2'>
                <FieldDescription>
                  {t('Failed to load API keys')}
                </FieldDescription>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={keys.isFetching}
                  onClick={() => void keys.refetch()}
                >
                  {t('Retry')}
                </Button>
              </div>
            )}
            {keys.isSuccess && !options.length && (
              <FieldDescription>
                {t('No enabled API keys match this group filter.')}
              </FieldDescription>
            )}
            {errors.keyId && (
              <FieldError id={`${id}-key-error`}>
                {t('Select an API key')}
              </FieldError>
            )}
          </Field>
          {endpoint === 'alpha/search' && (
            <Field data-invalid={!!errors.model}>
              <FieldLabel htmlFor={`${id}-model`}>{t('Model')}</FieldLabel>
              <Input
                id={`${id}-model`}
                required
                disabled={isSubmitting}
                aria-invalid={!!errors.model}
                aria-describedby={
                  errors.model ? `${id}-model-error` : undefined
                }
                {...form.register('model')}
              />
              {errors.model && (
                <FieldError id={`${id}-model-error`}>
                  {t('Enter an Alpha Search model.')}
                </FieldError>
              )}
            </Field>
          )}
          <Field data-invalid={!!errors.input}>
            <FieldLabel htmlFor={`${id}-input`}>
              {fetchEndpoint ? t('URL') : t('Search query')}
            </FieldLabel>
            <Input
              id={`${id}-input`}
              type={fetchEndpoint ? 'url' : 'search'}
              required
              disabled={isSubmitting}
              aria-invalid={!!errors.input}
              aria-describedby={errors.input ? `${id}-input-error` : undefined}
              {...form.register('input')}
            />
            {errors.input && (
              <FieldError id={`${id}-input-error`}>{inputError}</FieldError>
            )}
          </Field>
          <div>
            <Button type='submit' disabled={isSubmitting || !options.length}>
              {isSubmitting ? t('Sending...') : t('Send request')}
            </Button>
          </div>
        </FieldGroup>
      </form>
      <Link to='/docs' hash='moonshot-service' search={{ group: group || undefined }} className='text-primary self-start underline'>
        {t('Generate Kimi Code config')}
      </Link>
      {failed && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'Could not send the tool request. Check the API key and connection.'
            )}
          </AlertDescription>
        </Alert>
      )}
      <div role='status'>
        {result && (
          <div className='flex flex-wrap items-baseline gap-2'>
            <h2 className='font-semibold'>{t('Response')}</h2>
            <span className='text-muted-foreground'>HTTP {result.status}</span>
            {!result.ok && (
              <span className='text-destructive'>{t('Request failed')}</span>
            )}
            {result.empty && result.ok && (
              <span className='text-muted-foreground'>{t('Empty result')}</span>
            )}
            {result.invalidFormat && (
              <span className='text-destructive'>
                {t('Unexpected response format')}
              </span>
            )}
          </div>
        )}
      </div>
      {result?.body && (
        <pre className='bg-muted overflow-x-auto rounded-md p-4 text-sm break-words whitespace-pre-wrap'>
          <code data-format={result.format}>{result.body}</code>
        </pre>
      )}
    </div>
  )
}
