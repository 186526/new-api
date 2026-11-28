import { useQuery } from '@tanstack/react-query'
import { ChevronDown, Plus, Shield, Trash2 } from 'lucide-react'
import { useId, useRef, useState, type ReactNode } from 'react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Field,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

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
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const piiRules = redactionRuleIDs.slice(0, 6)
  const secretRules = redactionRuleIDs.slice(6)
  const activeRuleCount =
    config.builtin_rules.length + config.custom_rules.length
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

  const isOpen = !!errors || (expanded ?? enabled)

  return (
    <SideDrawerSection>
      <Collapsible open={isOpen} onOpenChange={setExpanded}>
        <CollapsibleTrigger
          render={
            <button
              type='button'
              className='hover:bg-muted/40 flex w-full items-center gap-3 rounded-md py-1.5 text-left transition-colors'
            />
          }
        >
          <SideDrawerSectionHeader
            className='flex-1'
            icon={<Shield className='size-4' />}
            title={t('Request redaction')}
            description={t('Redact requests before forwarding')}
          />
          <Badge variant={enabled ? 'default' : 'outline'} className='shrink-0'>
            {enabled
              ? t('{{count}} rules', { count: activeRuleCount })
              : t('Disabled')}
          </Badge>
          <ChevronDown
            className={cn(
              'text-muted-foreground size-4 shrink-0 transition-transform',
              isOpen && 'rotate-180'
            )}
          />
        </CollapsibleTrigger>
        <CollapsibleContent className='space-y-4 pt-3'>
          {props.children}
          <div className={sideDrawerSwitchItemClassName()}>
            <div className='flex flex-col gap-0.5'>
              <FieldLabel htmlFor={`${id}-enable`} className='text-sm'>
                {t('Redact requests before forwarding')}
              </FieldLabel>
              <span className='text-muted-foreground text-xs'>
                {t('Replace personal info and credentials before upstream dispatch.')}
              </span>
            </div>
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
          </div>
          {guardrails?.available === false && (
            <p className='text-muted-foreground text-xs'>
              {t('Request redaction is currently unavailable')}
            </p>
          )}
          {enabled && (
            <div className='space-y-4'>
              {(errors?.strategy?.message ||
                errors?.scopes?.message ||
                errors?.builtin_rules?.message ||
                errors?.custom_rules?.message ||
                errors?.restore_pii_in_response?.message) && (
                <ul
                  role='alert'
                  className='bg-destructive/10 text-destructive rounded-md border border-destructive/20 p-2.5 text-xs'
                >
                  {(
                    [
                      'strategy',
                      'scopes',
                      'builtin_rules',
                      'custom_rules',
                      'restore_pii_in_response',
                    ] as const
                  ).map((field) => {
                    const message = errors?.[field]?.message
                    return message ? <li key={field}>{message}</li> : null
                  })}
                </ul>
              )}

              <div className='space-y-2'>
                <span className='text-sm font-medium'>{t('Replacement style')}</span>
                <RadioGroup
                  value={config.strategy}
                  onValueChange={(value) => {
                    const strategy = value as RedactionConfig['strategy']
                    update({
                      strategy,
                      restore_pii_in_response:
                        strategy === 'typed_placeholder' &&
                        config.restore_pii_in_response,
                    })
                  }}
                  className='grid gap-2 sm:grid-cols-3'
                >
                  {(
                    [
                      ['typed_placeholder', t('Typed placeholder'), '<EMAIL_1>'],
                      ['mask', t('Fixed mask'), '[REDACTED]'],
                      ['hmac_short', t('Short fingerprint'), '<EMAIL#a93f>'],
                    ] as const
                  ).map(([strategy, label, example]) => (
                    <label
                      key={strategy}
                      htmlFor={`${id}-${strategy}`}
                      className={cn(
                        'flex cursor-pointer flex-col gap-1 rounded-lg border p-2.5 transition-colors',
                        config.strategy === strategy
                          ? 'border-primary bg-primary/5'
                          : 'hover:bg-muted/40'
                      )}
                    >
                      <div className='flex items-center gap-2'>
                        <RadioGroupItem value={strategy} id={`${id}-${strategy}`} />
                        <span className='text-xs font-medium'>{label}</span>
                      </div>
                      <code className='text-muted-foreground ps-6 text-[11px]'>
                        {example}
                      </code>
                    </label>
                  ))}
                </RadioGroup>
              </div>

              <div className='space-y-2.5 rounded-lg border p-3'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <span className='text-sm font-medium'>{t('Redaction rules')}</span>
                  <div className='flex flex-wrap gap-1.5'>
                    <Button
                      type='button'
                      variant='outline'
                      size='xs'
                      onClick={() => update({ builtin_rules: [...redactionRuleIDs] })}
                    >
                      {t('All protection')}
                    </Button>
                    <Button
                      type='button'
                      variant='outline'
                      size='xs'
                      onClick={() =>
                        update({
                          builtin_rules: [...secretRules],
                          restore_pii_in_response: false,
                        })
                      }
                    >
                      {t('Secrets only')}
                    </Button>
                    <Button
                      type='button'
                      variant='outline'
                      size='xs'
                      onClick={() => update({ builtin_rules: [...piiRules] })}
                    >
                      {t('Personal information only')}
                    </Button>
                  </div>
                </div>

                <div className='space-y-2'>
                  <p className='text-muted-foreground text-xs font-medium'>
                    {t('Personal information')}
                  </p>
                  <div className='grid grid-cols-1 gap-2 sm:grid-cols-2'>
                    {piiRules.map((rule) => (
                      <label
                        key={rule}
                        htmlFor={`${id}-${rule}`}
                        className='hover:bg-muted/40 flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-xs'
                      >
                        <Checkbox
                          id={`${id}-${rule}`}
                          checked={config.builtin_rules.includes(rule)}
                          onCheckedChange={(checked) =>
                            update({
                              builtin_rules: checked
                                ? [...config.builtin_rules, rule]
                                : config.builtin_rules.filter((entry) => entry !== rule),
                            })
                          }
                        />
                        <span>{ruleLabels[rule]}</span>
                      </label>
                    ))}
                  </div>
                </div>

                <div className='space-y-2'>
                  <p className='text-muted-foreground text-xs font-medium'>
                    {t('Credentials & secrets')}
                  </p>
                  <div className='grid grid-cols-1 gap-2 sm:grid-cols-2'>
                    {secretRules.map((rule) => (
                      <label
                        key={rule}
                        htmlFor={`${id}-${rule}`}
                        className='hover:bg-muted/40 flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-xs'
                      >
                        <Checkbox
                          id={`${id}-${rule}`}
                          checked={config.builtin_rules.includes(rule)}
                          onCheckedChange={(checked) =>
                            update({
                              builtin_rules: checked
                                ? [...config.builtin_rules, rule]
                                : config.builtin_rules.filter((entry) => entry !== rule),
                            })
                          }
                        />
                        <span>{ruleLabels[rule]}</span>
                      </label>
                    ))}
                  </div>
                </div>
              </div>

              <div className='space-y-2.5 rounded-lg border p-3'>
                <div className='flex items-center justify-between gap-2'>
                  <span className='text-sm font-medium'>{t('Custom RE2 rules')}</span>
                  <Button
                    type='button'
                    variant='outline'
                    size='xs'
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
                    <Plus className='mr-1 size-3' aria-hidden='true' />
                    {t('Add rule')}
                  </Button>
                </div>
                {config.custom_rules.length > 0 && (
                  <div className='space-y-2'>
                    {config.custom_rules.map((rule, index) => {
                      const labelError =
                        form.formState.errors.redaction_config?.custom_rules?.[index]?.label?.message
                      const patternError =
                        form.formState.errors.redaction_config?.custom_rules?.[index]?.pattern?.message
                      const replacementError =
                        form.formState.errors.redaction_config?.custom_rules?.[index]?.replacement?.message
                      const rowError = labelError || patternError || replacementError
                      return (
                        <div key={rule.id} className='space-y-1'>
                          <div className='grid grid-cols-1 gap-1.5 sm:grid-cols-[1fr_1.5fr_1fr_auto] sm:items-center'>
                            <Input
                              id={`${id}-${rule.id}-label`}
                              aria-label={t('Rule label')}
                              placeholder={t('Rule label')}
                              value={rule.label ?? ''}
                              spellCheck={false}
                              autoComplete='off'
                              aria-invalid={!!labelError}
                              className='h-8 text-xs'
                              onChange={(event) =>
                                update({
                                  custom_rules: config.custom_rules.map((entry, position) =>
                                    position === index
                                      ? { ...entry, label: event.target.value }
                                      : entry
                                  ),
                                })
                              }
                            />
                            <Input
                              id={`${id}-${rule.id}-pattern`}
                              aria-label={t('RE2 pattern')}
                              placeholder={t('RE2 pattern')}
                              value={rule.pattern ?? ''}
                              spellCheck={false}
                              autoComplete='off'
                              aria-invalid={!!patternError}
                              className='h-8 font-mono text-xs'
                              onChange={(event) =>
                                update({
                                  custom_rules: config.custom_rules.map((entry, position) =>
                                    position === index
                                      ? { ...entry, pattern: event.target.value }
                                      : entry
                                  ),
                                })
                              }
                            />
                            <Input
                              id={`${id}-${rule.id}-replacement`}
                              aria-label={t('Fixed replacement')}
                              placeholder={t('Fixed replacement')}
                              value={rule.replacement ?? ''}
                              spellCheck={false}
                              autoComplete='off'
                              aria-invalid={!!replacementError}
                              className='h-8 text-xs'
                              onChange={(event) =>
                                update({
                                  custom_rules: config.custom_rules.map((entry, position) =>
                                    position === index
                                      ? { ...entry, replacement: event.target.value }
                                      : entry
                                  ),
                                })
                              }
                            />
                            <Button
                              type='button'
                              variant='ghost'
                              size='icon-xs'
                              aria-label={`${t('Remove rule')} ${index + 1}`}
                              className='text-muted-foreground hover:text-destructive justify-self-end'
                              onClick={() =>
                                update({
                                  custom_rules: config.custom_rules.filter(
                                    (entry) => entry.id !== rule.id
                                  ),
                                })
                              }
                            >
                              <Trash2 className='size-3.5' aria-hidden='true' />
                            </Button>
                          </div>
                          {rowError && (
                            <p className='text-destructive text-xs'>{rowError}</p>
                          )}
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>

              <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
                <div className='rounded-lg border px-3 py-2'>
                  <CollapsibleTrigger
                    render={
                      <button
                        type='button'
                        className='flex w-full items-center justify-between py-1 text-left text-xs font-medium'
                      />
                    }
                  >
                    <span>{t('Scopes, allowlist & response restore')}</span>
                    <ChevronDown
                      className={cn(
                        'text-muted-foreground size-3.5 transition-transform',
                        advancedOpen && 'rotate-180'
                      )}
                    />
                  </CollapsibleTrigger>
                  <CollapsibleContent className='space-y-3 pt-2.5'>
                    <div className='space-y-1.5'>
                      <span className='text-muted-foreground text-xs font-medium'>
                        {t('Request fields')}
                      </span>
                      <div className='flex flex-wrap gap-3'>
                        {(
                          [
                            ['user_messages', t('User messages')],
                            ['tool_io', t('Tool inputs and outputs')],
                            ['system_prompt', t('System prompt')],
                          ] as const
                        ).map(([scope, label]) => (
                          <label
                            key={scope}
                            htmlFor={`${id}-${scope}`}
                            className='flex cursor-pointer items-center gap-2 text-xs'
                          >
                            <Checkbox
                              id={`${id}-${scope}`}
                              checked={config.scopes[scope]}
                              onCheckedChange={(checked) =>
                                update({
                                  scopes: {
                                    ...config.scopes,
                                    [scope]: !!checked,
                                  },
                                })
                              }
                            />
                            <span>{label}</span>
                          </label>
                        ))}
                      </div>
                    </div>

                    <div className={sideDrawerSwitchItemClassName()}>
                      <div className='flex flex-col gap-0.5'>
                        <FieldLabel htmlFor={`${id}-restore`} className='text-xs'>
                          {t('Restore supported personal information in responses')}
                        </FieldLabel>
                        <span id={`${id}-restore-help`} className='text-muted-foreground text-[11px]'>
                          {guardrails?.allow_response_restore
                            ? t('Only supported personal information is restored. Credentials are never restored.')
                            : t('Response restoration is currently unavailable')}
                        </span>
                      </div>
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
                    </div>

                    <Field>
                      <FieldLabel htmlFor={`${id}-allowlist`} className='text-xs'>
                        {t('Allowlist, one entry per line')}
                      </FieldLabel>
                      <Textarea
                        key={id}
                        id={`${id}-allowlist`}
                        rows={2}
                        defaultValue={config.allowlist.join('\n')}
                        aria-invalid={!!errors?.allowlist?.message}
                        aria-describedby={
                          errors?.allowlist?.message ? `${id}-allowlist-error` : undefined
                        }
                        spellCheck={false}
                        autoComplete='off'
                        className='min-h-14 text-xs'
                        onChange={(event) =>
                          update({
                            allowlist: event.target.value.split('\n').filter(Boolean),
                          })
                        }
                      />
                      {errors?.allowlist?.message && (
                        <p id={`${id}-allowlist-error`} className='text-destructive text-xs'>
                          {errors.allowlist.message}
                        </p>
                      )}
                    </Field>
                  </CollapsibleContent>
                </div>
              </Collapsible>

              <Field className='bg-muted/20 rounded-lg border p-3'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <FieldLabel htmlFor={`${id}-preview`} className='text-xs font-medium'>
                    {t('Preview sample')}
                  </FieldLabel>
                  <div className='flex flex-wrap gap-1.5'>
                    <Button
                      type='button'
                      variant='ghost'
                      size='xs'
                      onClick={() => {
                        setText(sample)
                        invalidatePreview()
                      }}
                    >
                      {t('Use sample')}
                    </Button>
                    <Button
                      type='button'
                      variant='secondary'
                      size='xs'
                      disabled={!text || pending}
                      onClick={() => void preview()}
                    >
                      {t('Preview redaction')}
                    </Button>
                  </div>
                </div>
                <Textarea
                  id={`${id}-preview`}
                  rows={2}
                  value={text}
                  maxLength={8192}
                  autoComplete='off'
                  className='bg-background min-h-14 text-xs'
                  onChange={(event) => {
                    setText(event.target.value)
                    invalidatePreview()
                  }}
                />
                <output
                  className='text-xs break-words whitespace-pre-wrap'
                  aria-live='polite'
                >
                  {hits !== null && (
                    <span className='mb-1 block font-medium'>
                      {t('Redaction matches: {{count}}', { count: hits })}
                    </span>
                  )}
                  {hits !== null && (
                    <span className='text-muted-foreground mb-1 block'>
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
            </div>
          )}
        </CollapsibleContent>
      </Collapsible>
    </SideDrawerSection>
  )
}
