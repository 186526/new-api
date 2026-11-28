import { useQuery } from '@tanstack/react-query'
import { Shield } from 'lucide-react'
import { useId, useRef, useState, type ReactNode } from 'react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldSet,
  FieldLegend,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { attachRedactionError, redactionErrorMessage } from '../lib/redaction-feedback'

import {
  DEFAULT_REDACTION_CONFIG,
  type ApiKeyFormValues,
} from '../lib/api-key-form'
import {
  redactionRuleIDs,
  type ApiResponse,
  type RedactionConfig,
} from '../types'

const sample = 'Email: alice@example.invalid'

type PreviewSegment = { text: string; rule?: string }

function renderPreviewSegments(segments: PreviewSegment[]) {
  let offset = 0
  return segments.filter((segment) => segment.text.length > 0).map((segment) => {
    const start = offset
    offset += segment.text.length
    return segment.rule ? (
      <mark key={start} className='bg-accent text-accent-foreground'>
        {segment.text}
      </mark>
    ) : (
      <span key={start}>{segment.text}</span>
    )
  })
}

export function ApiKeyRedactionSection(props: { open: boolean; children?: ReactNode }) {
  const { t } = useTranslation()
  const id = useId()
  const form = useFormContext<ApiKeyFormValues>()
  const enabled =
    useWatch({ control: form.control, name: 'redaction_enabled' }) ?? false
  const config =
    useWatch({ control: form.control, name: 'redaction_config' }) ??
    DEFAULT_REDACTION_CONFIG
  const [text, setText] = useState('')
  const [result, setResult] = useState('')
  const [hits, setHits] = useState<number | null>(null)
  const [counts, setCounts] = useState<Record<string, number>>({})
  const [segments, setSegments] = useState<{ text: string; rule?: string }[]>([])
  const [expanded, setExpanded] = useState<boolean | undefined>(undefined)
  const ruleLabels = {
    email: t('Email addresses'),
    phone: t('Phone numbers'),
    cn_id: t('Chinese ID numbers'),
    credit_card: t('Payment card numbers'),
    private_ip: t('Private IPv4 addresses'),
    all_ip: t('All IPv4 addresses'),
    api_keys: t('API keys and access tokens'),
    cloud_tokens: t('Cloud credentials'),
    dsn_secret: t('Database connection passwords'),
    jwt_bearer: t('Bearer tokens and JWTs'),
    pem_private_key: t('PEM private keys'),
  }
  const [pending, setPending] = useState(false)
  const guardrails = useQuery({
    queryKey: ['token-redaction-guardrails'],
    enabled: props.open,
    retry: false,
    queryFn: async () =>
      requireServerSuccess(
        (
          await api.get<
            ApiResponse<{
              available: boolean
              max_custom_rules_per_key: number
              allow_response_restore: boolean
            }>
          >('/api/token/redaction/guardrails')
        ).data
      ),
  }).data?.data
  const previewGeneration = useRef(0)
  const errors = form.formState.errors.redaction_config
  const invalidatePreview = () => {
    previewGeneration.current++
    setResult('')
    setHits(null)
  }
  const update = (patch: Partial<RedactionConfig>) => {
    form.clearErrors('redaction_config')
    form.setValue('redaction_config', { ...config, ...patch }, { shouldDirty: true })
    invalidatePreview()
  }

  async function preview() {
    if (pending) return
    invalidatePreview()
    const generation = previewGeneration.current
    form.clearErrors('redaction_config')
    setPending(true)
    const fallback = t('Could not preview request redaction')
    try {
      const { data } = await api.post<
        ApiResponse<{ text: string; counts: Record<string, number>; segments?: PreviewSegment[] }>
      >('/api/token/redaction/preview', { config, text })
      if (generation !== previewGeneration.current) return
      if (data.success && data.data) {
        setResult(data.data.text)
        setSegments(data.data.segments ?? [])
        setCounts(data.data.counts)
        setHits(
          Object.values(data.data.counts).reduce((sum, count) => sum + count, 0)
        )
      } else {
        const message = redactionErrorMessage(t, data, fallback)
        attachRedactionError(form.setError, data, message)
        setResult(message)
      }
    } catch {
      if (generation === previewGeneration.current) setResult(fallback)
    } finally {
      setPending(false)
    }
  }

  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        icon={<Shield />}
        title={t('Request redaction')}
      />
      {props.children}
      <Collapsible open={!!errors || (expanded ?? enabled)} onOpenChange={setExpanded}>
        <CollapsibleTrigger type='button' render={<Button variant='outline' />}>
          {t('Configure redaction')}
        </CollapsibleTrigger>
        <CollapsibleContent className='pt-3'>
      <FieldGroup>
        <Field orientation='horizontal'>
          <Switch
            id={`${id}-enable`}
            checked={enabled}
            disabled={!enabled && !guardrails?.available}
            onCheckedChange={(checked) => {
              form.setValue('redaction_enabled', checked, { shouldDirty: true })
              if (checked && !form.getValues('redaction_config')) {
                update(DEFAULT_REDACTION_CONFIG)
              }
            }}
          />
          <FieldLabel htmlFor={`${id}-enable`}>
            {t('Redact requests before forwarding')}
          </FieldLabel>
        </Field>
        {guardrails?.available === false && (
          <p className='text-muted-foreground text-sm'>
            {t('Request redaction is currently unavailable')}
          </p>
        )}
        {enabled && (
          <>
            {(errors?.strategy?.message || errors?.scopes?.message || errors?.builtin_rules?.message || errors?.custom_rules?.message || errors?.restore_pii_in_response?.message) && (
              <ul role='alert' className='text-destructive text-sm'>
                {(['strategy', 'scopes', 'builtin_rules', 'custom_rules', 'restore_pii_in_response'] as const).map((field) => {
                  const message = errors?.[field]?.message
                  return message ? <li key={field}>{message}</li> : null
                })}
              </ul>
            )}
            <FieldSet>
              <FieldLegend>{t('Replacement style')}</FieldLegend>
              <FieldGroup>
                {(
                  [
                    ['typed_placeholder', t('Typed placeholder')],
                    ['mask', t('Fixed mask')],
                    ['hmac_short', t('Short fingerprint')],
                  ] as const
                ).map(([strategy, label]) => (
                  <Field orientation='horizontal' key={strategy}>
                    <Input
                      className='size-4'
                      type='radio'
                      name={`${id}-strategy`}
                      id={`${id}-${strategy}`}
                      checked={config.strategy === strategy}
                      onChange={() =>
                        update({
                          strategy,
                          restore_pii_in_response:
                            strategy === 'typed_placeholder' &&
                            config.restore_pii_in_response,
                        })
                      }
                    />
                    <FieldLabel htmlFor={`${id}-${strategy}`}>
                      {label}
                    </FieldLabel>
                  </Field>
                ))}
              </FieldGroup>
            </FieldSet>
            <FieldSet>
              <FieldLegend>{t('Redaction rules')}</FieldLegend>
              <div className='flex flex-wrap gap-2'>
                <Button type='button' variant='outline' size='sm' onClick={() =>
                  update({ builtin_rules: [...redactionRuleIDs] })
                }>
                  {t('All protection')}
                </Button>
                <Button type='button' variant='outline' size='sm' onClick={() =>
                  update({ builtin_rules: redactionRuleIDs.slice(6), restore_pii_in_response: false })
                }>
                  {t('Secrets only')}
                </Button>
                <Button type='button' variant='outline' size='sm' onClick={() =>
                  update({ builtin_rules: redactionRuleIDs.slice(0, 6) })
                }>
                  {t('Personal information only')}
                </Button>
              </div>
              <FieldGroup className='grid grid-cols-1 gap-2 sm:grid-cols-2'>
                {redactionRuleIDs.map((rule) => (
                  <Field orientation='horizontal' key={rule}>
                    <Input
                      className='size-4'
                      type='checkbox'
                      id={`${id}-${rule}`}
                      checked={config.builtin_rules.includes(rule)}
                      onChange={(event) =>
                        update({
                          builtin_rules: event.target.checked
                            ? [...config.builtin_rules, rule]
                            : config.builtin_rules.filter(
                                (entry) => entry !== rule
                              ),
                        })
                      }
                    />
                    <FieldLabel htmlFor={`${id}-${rule}`}>
                      {ruleLabels[rule]}
                    </FieldLabel>
                  </Field>
                ))}
              </FieldGroup>
            </FieldSet>
            <FieldSet>
              <FieldLegend>{t('Request fields')}</FieldLegend>
              <FieldGroup>
                {(
                  [
                    ['user_messages', t('User messages')],
                    ['tool_io', t('Tool inputs and outputs')],
                    ['system_prompt', t('System prompt')],
                  ] as const
                ).map(([scope, label]) => (
                  <Field orientation='horizontal' key={scope}>
                    <Input
                      className='size-4'
                      type='checkbox'
                      id={`${id}-${scope}`}
                      checked={config.scopes[scope]}
                      onChange={(event) =>
                        update({
                          scopes: {
                            ...config.scopes,
                            [scope]: event.target.checked,
                          },
                        })
                      }
                    />
                    <FieldLabel htmlFor={`${id}-${scope}`}>{label}</FieldLabel>
                  </Field>
                ))}
              </FieldGroup>
            </FieldSet>
            <Field orientation='horizontal'>
              <Switch
                id={`${id}-restore`}
                aria-describedby={`${id}-restore-help`}
                checked={config.restore_pii_in_response}
                disabled={
                  !config.restore_pii_in_response &&
                  (config.strategy !== 'typed_placeholder' ||
                    !guardrails?.allow_response_restore)
                }
                onCheckedChange={(checked) =>
                  update({ restore_pii_in_response: checked })
                }
              />
              <FieldLabel htmlFor={`${id}-restore`}>
                {t('Restore supported personal information in responses')}
              </FieldLabel>
            </Field>
            <p id={`${id}-restore-help`} className='text-muted-foreground text-sm'>
              {guardrails?.allow_response_restore
                ? t('Only supported personal information is restored. Credentials are never restored.')
                : t('Response restoration is currently unavailable')}
            </p>
            <Field>
              <FieldLabel htmlFor={`${id}-allowlist`}>
                {t('Allowlist, one entry per line')}
              </FieldLabel>
              <Textarea
                key={id}
                id={`${id}-allowlist`}
                defaultValue={config.allowlist.join('\n')}
                aria-invalid={!!errors?.allowlist?.message}
                aria-describedby={errors?.allowlist?.message ? `${id}-allowlist-error` : undefined}
                spellCheck={false}
                autoComplete='off'
                onChange={(event) =>
                  update({
                    allowlist: event.target.value.split('\n').filter(Boolean),
                  })
                }
              />
              {errors?.allowlist?.message && (
                <p id={`${id}-allowlist-error`} className='text-destructive text-sm'>
                  {errors.allowlist.message}
                </p>
              )}
            </Field>
            <FieldSet>
              <FieldLegend>{t('Custom RE2 rules')}</FieldLegend>
              <FieldGroup>
                {config.custom_rules.map((rule, index) => (
                  <FieldSet key={rule.id}>
                    <FieldLegend>
                      {t('Rule {{number}}', { number: index + 1 })}
                    </FieldLegend>
                    <FieldGroup>
                      {(
                        [
                          ['label', t('Rule label')],
                          ['pattern', t('RE2 pattern')],
                          ['replacement', t('Fixed replacement')],
                        ] as const
                      ).map(([key, label]) => {
                        const fieldId = `${id}-${rule.id}-${key}`
                        const error =
                          form.formState.errors.redaction_config
                            ?.custom_rules?.[index]?.[key]?.message
                        return (
                          <Field key={key} data-invalid={!!error}>
                            <FieldLabel htmlFor={fieldId}>{label}</FieldLabel>
                            <Input
                              id={fieldId}
                              value={rule[key] ?? ''}
                              spellCheck={false}
                              autoComplete='off'
                              aria-invalid={!!error}
                              aria-describedby={
                                error ? `${fieldId}-error` : undefined
                              }
                              onChange={(event) =>
                                update({
                                  custom_rules: config.custom_rules.map(
                                    (entry, position) =>
                                      position === index
                                        ? {
                                            ...entry,
                                            [key]: event.target.value,
                                          }
                                        : entry
                                  ),
                                })
                              }
                            />
                            {error && (
                              <p
                                id={`${fieldId}-error`}
                                className='text-destructive text-sm'
                              >
                                {error}
                              </p>
                            )}
                          </Field>
                        )
                      })}
                      <Button
                        type='button'
                        variant='outline'
                        onClick={() =>
                          update({
                            custom_rules: config.custom_rules.filter(
                              (entry) => entry.id !== rule.id
                            ),
                          })
                        }
                      >
                        {t('Remove rule')}
                      </Button>
                    </FieldGroup>
                  </FieldSet>
                ))}
                <Button
                  type='button'
                  variant='outline'
                  disabled={
                    config.custom_rules.length >=
                    (guardrails?.max_custom_rules_per_key ?? 0)
                  }
                  onClick={() =>
                    update({
                      custom_rules: [
                        ...config.custom_rules,
                        { id: crypto.randomUUID(), label: '', pattern: '' },
                      ],
                    })
                  }
                >
                  {t('Add rule')}
                </Button>
              </FieldGroup>
            </FieldSet>
            <Field>
              <FieldLabel htmlFor={`${id}-preview`}>
                {t('Preview sample')}
              </FieldLabel>
              <Textarea
                id={`${id}-preview`}
                value={text}
                maxLength={8192}
                autoComplete='off'
                onChange={(event) => {
                  setText(event.target.value)
                  invalidatePreview()
                }}
              />
              <div className='flex flex-wrap gap-2'>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => {
                    setText(sample)
                    invalidatePreview()
                  }}
                >
                  {t('Use sample')}
                </Button>
                <Button
                  type='button'
                  variant='outline'
                  disabled={!text || pending}
                  onClick={() => void preview()}
                >
                  {t('Preview redaction')}
                </Button>
              </div>
              <output
                className='text-sm break-words whitespace-pre-wrap'
                aria-live='polite'
              >
                {hits !== null && (
                  <span className='block font-medium'>
                    {t('Redaction matches: {{count}}', { count: hits })}
                  </span>
                )}
                {hits !== null && (
                  <span className='block'>
                    {Object.entries(counts).map(([rule, count]) => (
                      <span key={rule} className='me-3'>
                        {rule === 'custom'
                          ? t('Custom RE2 rules')
                          : ruleLabels[rule as keyof typeof ruleLabels] ?? rule}: {count}
                      </span>
                    ))}
                  </span>
                )}
                {hits !== null && segments.length > 0
                  ? renderPreviewSegments(segments)
                  : result}
              </output>
            </Field>
          </>
        )}
      </FieldGroup>
        </CollapsibleContent>
      </Collapsible>
    </SideDrawerSection>
  )
}
