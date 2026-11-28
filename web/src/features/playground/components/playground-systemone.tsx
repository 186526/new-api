import { useQuery } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
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
  const [keyId, setKeyId] = useState('')
  const [model, setModel] = useState('jev-latest')
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
  const selectedKeyNum = Number(keyId)

  const modelsQuery = useQuery({
    queryKey: ['playground', 'systemone-models', selectedKeyNum],
    queryFn: ({ signal }) => listSystemOneModels(selectedKeyNum, signal),
    enabled: selectedKeyNum > 0,
    retry: false,
    meta: { errorToast: false },
  })
  const availableModels = modelsQuery.data ?? ['jev-latest']

  useEffect(() => {
    const list = keysQuery.data
    if (!keyId && list && list.length > 0) {
      const preferred = props.group
        ? list.find((k) => k.group === props.group)
        : undefined
      setKeyId(String((preferred ?? list[0]).id))
    }
  }, [keyId, keysQuery.data, props.group])

  useEffect(() => () => pending.current?.abort(), [])

  async function handleRun(event: React.FormEvent) {
    event.preventDefault()
    setErrorMessage('')
    setResult(undefined)
    if (!selectedKeyNum) {
      setErrorMessage(t('Select an API key first.'))
      return
    }
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
          keyId: selectedKeyNum,
          model: model.trim() || 'jev-latest',
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
            <CardTitle className='text-base'>
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
          <form onSubmit={(event) => void handleRun(event)} className='space-y-4'>
            <FieldGroup className='grid gap-4 sm:grid-cols-2'>
              <Field>
                <FieldLabel htmlFor={`${id}-key`}>{t('API Key')}</FieldLabel>
                <NativeSelect
                  id={`${id}-key`}
                  value={keyId}
                  disabled={keys.length === 0 || isRunning}
                  onChange={(event) => setKeyId(event.target.value)}
                >
                  {keys.length === 0 ? (
                    <NativeSelectOption value=''>
                      {t('No enabled API keys')}
                    </NativeSelectOption>
                  ) : (
                    keys.map((item) => (
                      <NativeSelectOption key={item.id} value={String(item.id)}>
                        {item.name} ({item.group})
                      </NativeSelectOption>
                    ))
                  )}
                </NativeSelect>
              </Field>
              <Field>
                <FieldLabel htmlFor={`${id}-model`}>{t('Model')}</FieldLabel>
                <Input
                  id={`${id}-model`}
                  list={`${id}-models-list`}
                  value={model}
                  disabled={isRunning}
                  onChange={(event) => setModel(event.target.value)}
                  placeholder='jev-latest'
                />
                <datalist id={`${id}-models-list`}>
                  {availableModels.map((entry) => (
                    <option key={entry} value={entry} />
                  ))}
                </datalist>
              </Field>
            </FieldGroup>

            <Field>
              <FieldLabel htmlFor={`${id}-state`}>{t('State (text or JSON)')}</FieldLabel>
              <Textarea
                id={`${id}-state`}
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
              <div className='flex flex-wrap items-center gap-4'>
                <label className='flex items-center gap-2 text-xs'>
                  <Switch
                    checked={longContext}
                    onCheckedChange={setLongContext}
                    disabled={isRunning}
                  />
                  <span>Long context</span>
                </label>
                <label className='flex items-center gap-2 text-xs'>
                  <Switch
                    checked={images}
                    onCheckedChange={setImages}
                    disabled={isRunning}
                  />
                  <span>Images</span>
                </label>
              </div>
              <Button type='submit' disabled={isRunning || !keyId}>
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
