import { skipToken, useQuery } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  listSystemOneModels,
  runSystemOneRequest,
  SYSTEM_ONE_PRESETS,
  type SystemOneRunResult,
} from '../lib/systemone-request'
import { listToolsKeys } from '../lib/tools-request'

export function PlaygroundSystemOne(props: { userId: number; group?: string }) {
  const { t } = useTranslation()
  const id = useId()
  const pending = useRef<AbortController | null>(null)
  const [keyId, setKeyId] = useState<number>()
  const [model, setModel] = useState('')
  const [stateText, setStateText] = useState(SYSTEM_ONE_PRESETS[0].state)
  const [questionsText, setQuestionsText] = useState(
    SYSTEM_ONE_PRESETS[0].questions
  )
  const [longContext, setLongContext] = useState(false)
  const [images, setImages] = useState(false)
  const [isRunning, setIsRunning] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')
  const [result, setResult] = useState<SystemOneRunResult>()

  const keysQuery = useQuery({
    queryKey: ['playground', 'tool-keys', props.userId],
    queryFn: ({ signal }) => listToolsKeys(signal),
    retry: false,
    meta: { errorToast: false },
  })
  const keys = keysQuery.data ?? []
  const selectedKey =
    keys.find((key) => key.id === keyId) ??
    keys.find((key) => key.group === props.group) ??
    keys[0]

  const modelsQuery = useQuery({
    queryKey: ['playground', 'systemone-models', props.userId, selectedKey?.id],
    queryFn: selectedKey
      ? ({ signal }) => listSystemOneModels(selectedKey.id, signal)
      : skipToken,
    retry: false,
    meta: { errorToast: false },
  })
  const modelsLoading =
    !!selectedKey && (modelsQuery.isPending || modelsQuery.isFetching)
  const confirmedModels =
    modelsQuery.isSuccess && !modelsLoading ? modelsQuery.data : undefined
  const selectedModel = confirmedModels?.includes(model)
    ? model
    : (confirmedModels?.[0] ?? '')

  useEffect(() => {
    if (selectedModel) setModel(selectedModel)
  }, [selectedModel])

  let keyStatus = ''
  if (!selectedKey) {
    if (keysQuery.isPending) {
      keyStatus = t('Loading...')
    } else if (keysQuery.isError) {
      keyStatus = t('Failed to load API keys')
    } else {
      keyStatus = t('No enabled API keys found. Create or enable one first.')
    }
  }

  let modelStatus = ''
  if (modelsLoading) {
    modelStatus = t('Loading System One models...')
  } else if (modelsQuery.isError) {
    modelStatus = t('Failed to load System One models.')
  } else if (confirmedModels?.length === 0) {
    modelStatus = t('No System One models available for this API key.')
  }

  useEffect(() => () => pending.current?.abort(), [])

  async function handleRun(event: React.FormEvent) {
    event.preventDefault()
    if (isRunning || !selectedKey || !selectedModel) return
    setErrorMessage('')
    setResult(undefined)
    let parsedQuestions: Record<string, unknown>
    try {
      const parsed: unknown = JSON.parse(questionsText)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('not object')
      }
      parsedQuestions = parsed as Record<string, unknown>
    } catch {
      setErrorMessage(t('Questions must be a valid JSON object.'))
      return
    }

    let parsedState: unknown = stateText
    const trimmedState = stateText.trim()
    if (
      (trimmedState.startsWith('{') && trimmedState.endsWith('}')) ||
      (trimmedState.startsWith('[') && trimmedState.endsWith(']'))
    ) {
      try {
        parsedState = JSON.parse(trimmedState)
      } catch {
        // Keep plain text when not valid JSON.
      }
    }

    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setIsRunning(true)
    try {
      const response = await runSystemOneRequest(
        {
          keyId: selectedKey.id,
          model: selectedModel,
          state: parsedState,
          questions: parsedQuestions,
          longContext,
          images,
        },
        controller.signal
      )
      if (!controller.signal.aborted) setResult(response)
    } catch (error) {
      if (!controller.signal.aborted) {
        setErrorMessage(
          error instanceof Error ? error.message : t('System One request failed')
        )
      }
    } finally {
      if (!controller.signal.aborted) setIsRunning(false)
    }
  }

  return (
    <div className='mx-auto flex w-full max-w-4xl flex-col gap-5 px-4 py-5 sm:px-6'>
      <Card>
        <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-2'>
          <div>
            <CardTitle className='text-base' id={`${id}-title`}>
              {t('System One (Jev) Playground')}
            </CardTitle>
            <p className='text-muted-foreground mt-1 text-xs'>
              {t(
                'Send typed decision questions to POST /systemone/v1/systemone using your API key.'
              )}
            </p>
          </div>
          <div className='flex flex-wrap gap-1.5'>
            {SYSTEM_ONE_PRESETS.map((preset) => (
              <Button
                key={preset.id}
                type='button'
                variant='outline'
                size='xs'
                onClick={() => {
                  setStateText(preset.state)
                  setQuestionsText(preset.questions)
                  setErrorMessage('')
                }}
              >
                {preset.label}
              </Button>
            ))}
          </div>
        </CardHeader>
        <CardContent>
          <form
            aria-labelledby={`${id}-title`}
            onSubmit={(event) => void handleRun(event)}
            className='flex flex-col gap-4'
          >
            <FieldGroup className='grid gap-4 sm:grid-cols-2'>
              <Field>
                <FieldLabel htmlFor={`${id}-key`}>{t('API Key')}</FieldLabel>
                <NativeSelect
                  id={`${id}-key`}
                  name='keyId'
                  value={selectedKey?.id ?? ''}
                  disabled={!selectedKey || isRunning}
                  onChange={(event) => setKeyId(Number(event.target.value))}
                >
                  {!selectedKey && (
                    <NativeSelectOption value=''>{keyStatus}</NativeSelectOption>
                  )}
                  {keys.map((item) => (
                    <NativeSelectOption key={item.id} value={item.id}>
                      {item.name} ({item.group})
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
              <Field>
                <FieldLabel htmlFor={`${id}-model`}>{t('Model')}</FieldLabel>
                <NativeSelect
                  id={`${id}-model`}
                  name='model'
                  value={selectedModel}
                  disabled={!selectedModel || isRunning}
                  aria-describedby={`${id}-model-status`}
                  onChange={(event) => setModel(event.target.value)}
                >
                  {!selectedModel && <NativeSelectOption value='' />}
                  {confirmedModels?.map((name) => (
                    <NativeSelectOption key={name} value={name}>
                      {name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <FieldDescription id={`${id}-model-status`} role='status'>
                  {modelStatus}
                </FieldDescription>
                {modelsQuery.isError && !modelsLoading && (
                  <Button
                    type='button'
                    variant='outline'
                    size='xs'
                    className='self-start'
                    onClick={() => void modelsQuery.refetch()}
                  >
                    {t('Retry')}
                  </Button>
                )}
              </Field>
            </FieldGroup>

            <Field>
              <FieldLabel htmlFor={`${id}-state`}>{t('State (text or JSON)')}</FieldLabel>
              <Textarea
                id={`${id}-state`}
                name='state'
                rows={3}
                value={stateText}
                disabled={isRunning}
                onChange={(event) => setStateText(event.target.value)}
              />
            </Field>

            <Field>
              <FieldLabel htmlFor={`${id}-questions`}>
                {t('Questions (JSON)')}
              </FieldLabel>
              <Textarea
                id={`${id}-questions`}
                name='questions'
                rows={8}
                spellCheck={false}
                value={questionsText}
                disabled={isRunning}
                onChange={(event) => setQuestionsText(event.target.value)}
                className='font-mono text-xs'
              />
              <FieldDescription>
                {t('Each question entry uses type: noul, choice, score, or box.')}
              </FieldDescription>
            </Field>

            <div className='flex flex-wrap items-center justify-between gap-3'>
              <FieldGroup className='w-auto flex-row flex-wrap gap-4'>
                <Field orientation='horizontal' className='w-auto'>
                  <Switch
                    id={`${id}-long-context`}
                    name='longContext'
                    checked={longContext}
                    onCheckedChange={setLongContext}
                    disabled={isRunning}
                    nativeButton
                    render={<button type='button' />}
                  />
                  <FieldLabel htmlFor={`${id}-long-context`}>
                    {t('Long context')}
                  </FieldLabel>
                </Field>
                <Field orientation='horizontal' className='w-auto'>
                  <Switch
                    id={`${id}-images`}
                    name='images'
                    checked={images}
                    onCheckedChange={setImages}
                    disabled={isRunning}
                    nativeButton
                    render={<button type='button' />}
                  />
                  <FieldLabel htmlFor={`${id}-images`}>{t('Images')}</FieldLabel>
                </Field>
              </FieldGroup>
              <Button type='submit' disabled={isRunning || !selectedModel}>
                {isRunning ? t('Running...') : t('Run System One')}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      {errorMessage && (
        <Alert variant='destructive'>
          <AlertDescription>{errorMessage}</AlertDescription>
        </Alert>
      )}

      {result && (
        <Card>
          <CardHeader className='flex flex-row items-center justify-between gap-2 py-3'>
            <CardTitle className='text-sm'>{t('Response')}</CardTitle>
            <Badge variant={result.ok ? 'default' : 'destructive'}>
              HTTP {result.status}
            </Badge>
          </CardHeader>
          <CardContent>
            <pre className='bg-muted/30 max-h-96 overflow-auto rounded-lg border p-3 font-mono text-xs'>
              {typeof result.body === 'string'
                ? result.body
                : JSON.stringify(result.body, null, 2)}
            </pre>
          </CardContent>
        </Card>
      )}
    </div>
  )
}
