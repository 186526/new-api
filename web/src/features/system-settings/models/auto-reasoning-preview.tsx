import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useId } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { Button } from '@/components/ui/button'
import { FieldGroup } from '@/components/ui/field'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { getUpstreamChannels } from '../api'
import { SettingsForm } from '../components/settings-form-layout'
import {
  type AutoReasoningPolicy,
  efforts,
  previewDraftDecision,
} from './auto-reasoning-policy'

const previewPath = '/api/system-settings/auto-reasoning/preview'
const protocols = ['chat', 'responses', 'claude', 'gemini'] as const
const byteLength = (text: string) => new TextEncoder().encode(text).length
const previewSchema = z
  .object({
    model: z
      .string()
      .trim()
      .min(1, 'Enter a model name.')
      .refine(
        (value) => byteLength(value) <= 256,
        'The model name exceeds 256 bytes.'
      ),
    group: z
      .string()
      .trim()
      .min(1, 'Enter a group.')
      .refine(
        (value) => byteLength(value) <= 64,
        'The group exceeds 64 bytes.'
      ),
    channel_id: z.string().regex(/^[1-9]\d*$/, 'Select a channel.'),
    protocol: z.enum(protocols),
    explicit_effort: z.enum(['', ...efforts]),
    mode: z.enum(['text', 'scale']),
    scale: z.string(),
    text: z.string(),
  })
  .superRefine((value, ctx) => {
    if (
      value.mode === 'text' &&
      (!value.text.trim() || byteLength(value.text) > 8192)
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['text'],
        message: 'Enter sample text of up to 8192 bytes.',
      })
    }
    if (
      value.mode === 'scale' &&
      (!/^\d+$/.test(value.scale) || Number(value.scale) > 10_000_000)
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['scale'],
        message: 'Enter a whole number from 0 to 10,000,000.',
      })
    }
  })

type PreviewForm = z.infer<typeof previewSchema>
type PreviewResult = {
  scale: number
  tier: number
  tier_effort: string
  effort: string
  reason: string
  fields: Record<string, string | number | boolean> | null
}

async function runPreview(draft: PreviewForm, policy: AutoReasoningPolicy) {
  const response = await api.post<{ success: boolean; data: PreviewResult }>(
    previewPath,
    {
      policy,
      model: draft.model,
      group: draft.group,
      channel_id: Number(draft.channel_id),
      protocol: draft.protocol,
      ...(draft.explicit_effort && { explicit_effort: draft.explicit_effort }),
      ...(draft.mode === 'text'
        ? { text: draft.text }
        : { scale: Number(draft.scale) }),
    }
  )
  return {
    policy: JSON.stringify(policy),
    result: requireServerSuccess(response.data).data,
  }
}

export function AutoReasoningPreview(props: {
  policy?: AutoReasoningPolicy
  unit: string
}) {
  const { t } = useTranslation()
  const headingId = useId()
  const form = useForm<PreviewForm>({
    resolver: zodResolver(previewSchema),
    defaultValues: {
      model: '',
      group: 'default',
      channel_id: '',
      protocol: 'responses',
      explicit_effort: '',
      mode: 'text',
      text: '',
      scale: '',
    },
  })
  const channels = useQuery({
    queryKey: ['automatic-reasoning', 'channels'],
    queryFn: async () =>
      requireServerSuccess(await getUpstreamChannels()).data.filter(
        (channel) => channel.id > 0
      ),
  })
  const preview = useMutation({
    mutationFn: (draft: PreviewForm) => {
      if (!props.policy) {
        throw new Error(
          t('Correct the reasoning rules before running a preview.')
        )
      }
      return runPreview(draft, props.policy)
    },
    gcTime: 0,
    meta: { errorToast: false },
  })
  const current = form.watch()
  const scaleMode = current.mode === 'scale'
  const submitted = preview.variables
  const sameInput =
    submitted &&
    submitted.model === current.model.trim() &&
    submitted.group === current.group.trim() &&
    submitted.channel_id === current.channel_id &&
    submitted.protocol === current.protocol &&
    submitted.explicit_effort === current.explicit_effort &&
    submitted.mode === current.mode &&
    (scaleMode
      ? submitted.scale === current.scale
      : submitted.text === current.text)
  const result =
    sameInput && preview.data?.policy === JSON.stringify(props.policy)
      ? preview.data?.result
      : undefined

  return (
    <div className='flex flex-col gap-4 border-t pt-4'>
      <h3 id={headingId} className='font-medium'>
        {t('Channel preview')}
      </h3>
      <p className='text-muted-foreground text-sm'>
        {t('Uses unsaved rules. No upstream request is sent.')}
      </p>
      <Form {...form}>
        <SettingsForm
          aria-labelledby={headingId}
          method='post'
          action={previewPath}
          noValidate
          onChange={() => preview.reset()}
          onSubmit={form.handleSubmit(
            (draft) => preview.mutate(draft),
            () => preview.reset()
          )}
        >
          <fieldset
            className='flex min-w-0 flex-col gap-4'
            disabled={preview.isPending}
          >
            <legend className='sr-only'>{t('Preview input')}</legend>
            <FieldGroup>
              <FormField
                control={form.control}
                name='channel_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Channel')}</FormLabel>
                    <FormControl>
                      <NativeSelect
                        {...field}
                        required
                        disabled={channels.isPending || !channels.data?.length}
                      >
                        <NativeSelectOption value=''>
                          {t('Select a channel')}
                        </NativeSelectOption>
                        {channels.data?.map((channel) => (
                          <NativeSelectOption
                            key={channel.id}
                            value={String(channel.id)}
                          >
                            {channel.name} (#{channel.id})
                          </NativeSelectOption>
                        ))}
                      </NativeSelect>
                    </FormControl>
                    {channels.isPending && (
                      <FormDescription>
                        {t('Loading channels…')}
                      </FormDescription>
                    )}
                    {channels.isError && (
                      <FormDescription>
                        {t('Could not load channels.')}
                      </FormDescription>
                    )}
                    {channels.isSuccess && channels.data.length === 0 && (
                      <FormDescription>
                        {t('No channels available.')}
                      </FormDescription>
                    )}
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='protocol'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Protocol')}</FormLabel>
                    <FormControl>
                      <NativeSelect {...field}>
                        <NativeSelectOption value='chat'>
                          {t('Chat Completions')}
                        </NativeSelectOption>
                        <NativeSelectOption value='responses'>
                          {t('Responses')}
                        </NativeSelectOption>
                        <NativeSelectOption value='claude'>
                          Claude
                        </NativeSelectOption>
                        <NativeSelectOption value='gemini'>
                          Gemini
                        </NativeSelectOption>
                      </NativeSelect>
                    </FormControl>
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='model'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Model name')}</FormLabel>
                    <FormControl>
                      <Input {...field} required autoComplete='off' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='group'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Group')}</FormLabel>
                    <FormControl>
                      <Input {...field} required autoComplete='off' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='explicit_effort'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Client reasoning effort')}</FormLabel>
                    <FormControl>
                      <NativeSelect {...field}>
                        <NativeSelectOption value=''>
                          {t('Not set')}
                        </NativeSelectOption>
                        {efforts.map((effort) => (
                          <NativeSelectOption key={effort} value={effort}>
                            {effort}
                          </NativeSelectOption>
                        ))}
                      </NativeSelect>
                    </FormControl>
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='mode'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Preview input')}</FormLabel>
                    <FormControl>
                      <NativeSelect {...field}>
                        <NativeSelectOption value='text'>
                          {t('Sample text')}
                        </NativeSelectOption>
                        <NativeSelectOption value='scale'>
                          {t('Input size')}
                        </NativeSelectOption>
                      </NativeSelect>
                    </FormControl>
                  </FormItem>
                )}
              />
              {scaleMode ? (
                <FormField
                  key='scale'
                  control={form.control}
                  name='scale'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Input size ({{unit}})', { unit: props.unit })}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={0}
                          max={10_000_000}
                          step={1}
                          required
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ) : (
                <FormField
                  key='text'
                  control={form.control}
                  name='text'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Sample text')}</FormLabel>
                      <FormControl>
                        <Textarea {...field} required maxLength={8192} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}
            </FieldGroup>
            <Button
              type='submit'
              variant='outline'
              className='self-start'
              disabled={!channels.data?.length}
            >
              {preview.isPending ? t('Running preview…') : t('Run preview')}
            </Button>
          </fieldset>
        </SettingsForm>
      </Form>
      <DraftDecision policy={props.policy} draft={current} />
      <p role='status' className='text-muted-foreground text-sm'>
        {preview.isError &&
          getServerErrorMessage(
            preview.error,
            t('Could not run the reasoning preview.')
          )}
        {result && t('Reasoning preview ready.')}
      </p>
      {result && <PreviewDetails result={result} unit={props.unit} />}
    </div>
  )
}

function DraftDecision(props: { policy?: AutoReasoningPolicy; draft: PreviewForm }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const heading = useId()
  const hint = useId()
  const { draft, policy } = props
  const scale = Number(draft.scale)
  const decision =
    policy?.enabled && draft.mode === 'scale' && /^\d+$/.test(draft.scale) && scale <= 10_000_000
      ? previewDraftDecision(policy, scale, draft.explicit_effort)
      : undefined
  return (
    <section aria-labelledby={heading} aria-describedby={hint} className='flex flex-col gap-2 text-sm'>
      <h4 id={heading} className='font-medium'>{t('Draft reasoning estimate')}</h4>
      <p id={hint} className='text-muted-foreground'>
        {t('This estimate excludes model suffixes, scope and channel settings.')}{' '}
        {t('Run the channel preview to inspect mapped reasoning fields.')}
      </p>
      <div aria-live='polite'>
        {decision ? (
          <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-1'>
            <dt>{t('Matched tier')}</dt>
            <dd>{decision.tier ? formatNumber(decision.tier, locale) : t('Overflow effort')}</dd>
            <dt>{t('Reasoning policy effort')}</dt>
            <dd>{decision.target}</dd>
            <dt>{t('Effective reasoning effort')}</dt>
            <dd>{decision.effort}</dd>
            <dt>{t('Reasoning conflict resolution')}</dt>
            <dd>{t(`reasoning.preview.reason.${decision.reason}`, { defaultValue: decision.reason })}</dd>
          </dl>
        ) : t('Enter a size to preview a tier.')}
      </div>
    </section>
  )
}

function PreviewDetails(props: { result: PreviewResult; unit: string }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { result } = props
  const fields = Object.entries(result.fields ?? {}).sort(([first], [second]) =>
    first.localeCompare(second)
  )
  return (
    <div className='flex min-w-0 flex-col gap-2 text-sm'>
      <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-1'>
        <dt>{t('Input size')}</dt>
        <dd>
          {formatNumber(result.scale, locale)} {props.unit}
        </dd>
        <dt>{t('Matched tier')}</dt>
        <dd>
          {result.tier
            ? formatNumber(result.tier, locale)
            : t('Overflow effort')}{' '}
          — {result.tier_effort}
        </dd>
        <dt>{t('Automatic effort')}</dt>
        <dd>{result.effort || t('Not applied')}</dd>
        <dt>{t('Reason')}</dt>
        <dd>
          {result.reason
            ? t(`reasoning.preview.reason.${result.reason}`, {
                defaultValue: result.reason,
              })
            : '—'}
        </dd>
      </dl>
      <h4 className='font-medium'>{t('Final outbound reasoning fields')}</h4>
      {fields.length ? (
        <pre className='bg-muted rounded-md p-3 [overflow-wrap:anywhere] whitespace-pre-wrap'>
          {JSON.stringify(Object.fromEntries(fields), null, 2)}
        </pre>
      ) : (
        <p className='text-muted-foreground'>
          {t('No outbound reasoning fields returned.')}
        </p>
      )}
    </div>
  )
}
