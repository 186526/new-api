import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import { getChannelCustomTools } from '../../api'
import {
  customToolsQueryKey,
  useCustomToolMutation,
} from '../../hooks/use-custom-tool-mutation'
import {
  customToolDefaultPath,
  customToolFormSchema,
  type CustomToolChange,
  type CustomToolFormValues,
  type CustomToolOperation,
  type CustomToolStatus,
} from '../../lib/custom-tools'

const operations = ['search', 'fetch'] as const

type OperationFormProps = {
  operation: CustomToolOperation
  status: CustomToolStatus
  canConfigure: boolean
  canVerify: boolean
  busy: boolean
  change: (change: CustomToolChange) => Promise<CustomToolStatus | undefined>
}

function CustomToolOperationForm(props: OperationFormProps) {
  const { t } = useTranslation()
  const id = useId()
  const [confirmRemove, setConfirmRemove] = useState(false)
  const savedPath = props.status.path ?? customToolDefaultPath(props.operation)
  const form = useForm<CustomToolFormValues>({
    resolver: zodResolver(customToolFormSchema(props.status.configured)),
    defaultValues: { path: savedPath, apiKey: '' },
  })
  const { control, getValues, resetField, setValue } = form
  const previousSavedPath = useRef(savedPath)
  const pathDraft = useWatch({ control, name: 'path' })
  const keyDraft = useWatch({ control, name: 'apiKey' })
  useEffect(() => {
    if (previousSavedPath.current === savedPath) return
    const draft = getValues('path')
    const preserveDraft = draft !== previousSavedPath.current
    previousSavedPath.current = savedPath
    resetField('path', { defaultValue: savedPath })
    if (preserveDraft) setValue('path', draft, { shouldDirty: true })
  }, [getValues, resetField, savedPath, setValue])
  const disabled = props.busy || !props.canConfigure
  const verifyDisabled =
    props.busy ||
    !props.canVerify ||
    !props.status.configured ||
    pathDraft !== savedPath ||
    Boolean(keyDraft)
  const pathError = form.formState.errors.path
  const keyError = form.formState.errors.apiKey
  const pathHelp = `${id}-path-help`
  const keyHelp = `${id}-key-help`
  let statusLabel = t('Not configured')
  if (props.status.configured) {
    statusLabel = props.status.verified ? t('Verified') : t('Not verified')
  }

  async function save(values: CustomToolFormValues): Promise<void> {
    form.resetField('apiKey')
    const saved = await props.change({
      operation: props.operation,
      action: 'save',
      path: values.path,
      apiKey: values.apiKey,
    })
    if (saved?.path) form.reset({ path: saved.path, apiKey: '' })
  }

  async function remove(): Promise<void> {
    const saved = await props.change({
      operation: props.operation,
      action: 'remove',
    })
    if (saved) {
      setConfirmRemove(false)
      form.reset({ path: customToolDefaultPath(props.operation), apiKey: '' })
    }
  }

  return (
    <form
      aria-labelledby={`${id}-heading`}
      method='post'
      noValidate
      onSubmit={(event) => {
        event.stopPropagation()
        void form.handleSubmit(save)(event)
      }}
    >
      <FieldGroup className='gap-4'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <h3 id={`${id}-heading`} className='font-medium'>
            {props.operation === 'search' ? t('Search') : t('Fetch')}
          </h3>
          <Badge variant={props.status.verified ? 'secondary' : 'outline'}>
            {statusLabel}
          </Badge>
        </div>
        <Field data-invalid={Boolean(pathError)} data-disabled={disabled}>
          <FieldLabel htmlFor={`${id}-path`}>{t('Endpoint path')}</FieldLabel>
          <Input
            {...form.register('path')}
            id={`${id}-path`}
            disabled={disabled}
            required
            maxLength={512}
            autoCapitalize='none'
            spellCheck={false}
            aria-invalid={Boolean(pathError)}
            aria-describedby={pathHelp}
          />
          {pathError ? (
            <FieldError id={pathHelp}>
              {t('Enter a path beginning with /.')}
            </FieldError>
          ) : (
            <FieldDescription id={pathHelp}>
              {props.status.path
                ? t('Saved path: {{path}}', {
                    path: props.status.path,
                    interpolation: { escapeValue: false },
                  })
                : t('Appended to the saved API address.')}
            </FieldDescription>
          )}
        </Field>
        {props.canConfigure && (
          <Field data-invalid={Boolean(keyError)} data-disabled={disabled}>
            <FieldLabel htmlFor={`${id}-key`}>{t('API Key')}</FieldLabel>
            <Input
              {...form.register('apiKey')}
              id={`${id}-key`}
              type='password'
              autoComplete='new-password'
              disabled={disabled}
              required={!props.status.configured}
              maxLength={4096}
              aria-invalid={Boolean(keyError)}
              aria-describedby={keyHelp}
            />
            {keyError ? (
              <FieldError id={keyHelp}>
                {t('Enter a valid API key.')}
              </FieldError>
            ) : (
              <FieldDescription id={keyHelp}>
                {props.status.configured
                  ? t('Leave blank to keep the existing key')
                  : t('Use a key for this operation.')}
              </FieldDescription>
            )}
          </Field>
        )}
        <div className='flex flex-wrap gap-2'>
          {props.canConfigure && (
            <Button type='submit' size='sm' disabled={disabled}>
              {t('Save')}
            </Button>
          )}
          {props.canVerify && (
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={verifyDisabled}
              onClick={() =>
                void props.change({
                  operation: props.operation,
                  action: 'verify',
                })
              }
            >
              {t('Verify')}
            </Button>
          )}
          {props.canConfigure && props.status.configured && (
            <Button
              type='button'
              size='sm'
              variant='ghost'
              disabled={disabled}
              onClick={() => setConfirmRemove(true)}
            >
              {t('Remove')}
            </Button>
          )}
        </div>
      </FieldGroup>
      {confirmRemove && (
        <ConfirmDialog
          open
          onOpenChange={setConfirmRemove}
          title={t('Remove tool configuration?')}
          desc={t('This removes the saved path and key for this operation.')}
          confirmText={t('Remove')}
          destructive
          isLoading={props.busy}
          handleConfirm={() => void remove()}
        />
      )}
    </form>
  )
}

type CustomToolsDialogProps = {
  channelId: number
  canConfigure: boolean
  canVerify: boolean
  onClose: () => void
}

type CustomToolsControlProps = Omit<
  CustomToolsDialogProps,
  'channelId' | 'onClose'
> & {
  channelId: number | null
  ready: boolean
  disabled: boolean
}

export function CustomToolsControl(props: CustomToolsControlProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  if (!props.ready || props.channelId === null) {
    return (
      <p className='text-muted-foreground text-sm'>
        {t('Save the channel before configuring tool endpoints.')}
      </p>
    )
  }
  return (
    <>
      <Button
        type='button'
        variant='outline'
        size='sm'
        disabled={props.disabled}
        onClick={() => setOpen(true)}
      >
        {t('Custom tool endpoints')}
      </Button>
      {open && (
        <CustomToolsDialog
          channelId={props.channelId}
          canConfigure={props.canConfigure}
          canVerify={props.canVerify}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

export function CustomToolsDialog(props: CustomToolsDialogProps) {
  return <CustomToolsDialogSession key={props.channelId} {...props} />
}

function CustomToolsDialogSession(props: CustomToolsDialogProps) {
  const { t } = useTranslation()
  const tools = useQuery({
    queryKey: customToolsQueryKey(props.channelId),
    queryFn: ({ signal }) => getChannelCustomTools(props.channelId, signal),
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })
  const { busy, change, feedback, requestInFlight } = useCustomToolMutation(
    props.channelId
  )
  const loading = busy || tools.isFetching

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !requestInFlight()) props.onClose()
      }}
      title={t('Custom tool endpoints')}
      description={t(
        'Save each operation before verifying it. Verification sends a sample request to the saved endpoint.'
      )}
      bodyClassName='flex flex-col gap-6'
    >
      <div role='status' className='text-sm'>
        {loading && t('Loading...')}
        {!loading && feedback && (
          <>
            {feedback.operation === 'search' ? t('Search') : t('Fetch')}:{' '}
            {feedback.result === 'updated'
              ? t('Tool configuration updated.')
              : t(
                  'Tool configuration changed. Review the saved settings and retry.'
                )}
          </>
        )}
      </div>
      {tools.isError && (
        <div role='alert' className='flex flex-wrap items-center gap-3'>
          <span>{t('Failed to load tool configuration.')}</span>
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={tools.isFetching || busy}
            onClick={() => void tools.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      )}
      {tools.data &&
        operations.map((operation) => (
          <CustomToolOperationForm
            key={operation}
            status={tools.data[operation]}
            operation={operation}
            canConfigure={props.canConfigure}
            canVerify={props.canVerify}
            busy={loading || tools.isError}
            change={change}
          />
        ))}
    </Dialog>
  )
}
