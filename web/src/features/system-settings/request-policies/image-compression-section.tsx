import { zodResolver } from '@hookform/resolvers/zod'
import { Link } from '@tanstack/react-router'
import { useEffect, useId } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
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
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'

import {
  SettingsControlGroup,
  SettingsForm,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { ImageCompressionPreview } from './image-compression-preview'
import { ImageCompressionScope } from './image-compression-scope'
import {
  imageCompressionDefaults,
  imageCompressionPrefix,
  imageCompressionSchema,
  type ImageCompressionSettings,
} from './image-compression-settings'
import { useSavePolicy } from './use-save-policy'

type Props = { defaultValues: ImageCompressionSettings; passthrough?: boolean }

export function ImageCompressionSection(props: Props) {
  const { t } = useTranslation()
  const qualityLabelId = useId()
  const save = useSavePolicy()
  const form = useForm<ImageCompressionSettings>({
    defaultValues: props.defaultValues,
    resolver: zodResolver(imageCompressionSchema),
  })
  const enabled = useWatch({ control: form.control, name: 'enabled' })
  const thresholdEnabled = useWatch({ control: form.control, name: 'threshold_enabled' })
  const remote = useWatch({
    control: form.control,
    name: 'compress_remote_urls',
  })
  const format = useWatch({ control: form.control, name: 'target_format' })
  const { dirtyFields } = form.formState
  useEffect(() => {
    form.reset(props.defaultValues, { keepDirtyValues: true })
  }, [form, props.defaultValues])

  const submit = form.handleSubmit(async (settings) => {
    const changed = Object.entries(settings).filter(
      ([key, value]) =>
        dirtyFields[key as keyof ImageCompressionSettings] &&
        value !== props.defaultValues[key as keyof ImageCompressionSettings]
    )
    if (changed.length) {
      try {
        await save.mutateAsync(
          Object.fromEntries(
            changed.map(([key, value]) => [
              imageCompressionPrefix + key,
              String(value),
            ])
          )
        )
        form.reset(settings)
      } catch {
        // The shared mutation displays the server error.
      }
    }
  })

  function preset(changes: Partial<ImageCompressionSettings>) {
    const keys = [
      'min_bytes_kb',
      'min_dimension',
      'max_dimension',
      'target_format',
      'jpeg_quality',
      'resampler',
    ] as const
    for (const key of keys) {
      form.setValue(key, changes[key] ?? imageCompressionDefaults[key], {
        shouldDirty: true,
      })
    }
    void form.trigger()
  }

  const fields = [
    ['min_bytes_kb', t('Size threshold (KB)')],
    ['min_dimension', t('Dimension threshold (px)')],
    ['max_dimension', t('Target max edge (px)')],
    ['jpeg_quality', t('JPEG quality')],
    ['max_process_images', t('Maximum images per request')],
    ['per_request_budget_ms', t('Compression budget (ms)')],
    ...(remote
      ? ([
          ['remote_timeout_ms', t('Download timeout (ms)')],
          ['remote_max_download_mb', t('Download limit (MB)')],
        ] as const)
      : []),
  ] as const

  return (
    <SettingsSection title={t('Image compression')}>
      <div className='flex flex-wrap gap-3 text-sm'>
        <Link to='/channels' className='text-primary underline underline-offset-4'>
          {t('Channel image limits')}
        </Link>
        <Link
          to='/system-settings/security/$section'
          params={{ section: 'rate-limit' }}
          className='text-primary underline underline-offset-4'
        >
          {t('Rate Limiting')}
        </Link>
      </div>
      {props.passthrough && (
        <Alert>
          <AlertDescription>
            {t('Requests in passthrough mode skip image compression.')}
          </AlertDescription>
        </Alert>
      )}
      <Form {...form}>
        <SettingsForm onSubmit={submit}>
          <SettingsPageFormActions
            onSave={submit}
            isSaving={form.formState.isSubmitting}
          />
          <SettingsControlGroup>
            {(
              [
                ['enabled', t('Enable image compression')],
                ['threshold_enabled', t('Use compression thresholds')],
                [
                  'skip_animated_and_svg',
                  t('Preserve animated images and SVG'),
                ],
                [
                  'compress_remote_urls',
                  t('Download and compress remote images'),
                ],
              ] as const
            ).map(([name, label]) => (
              <FormField
                key={name}
                control={form.control}
                name={name}
                render={({ field }) => (
                  <FormItem className='flex items-center justify-between gap-3'>
                    <div className='grid gap-1'>
                      <FormLabel>{label}</FormLabel>
                      {name === 'threshold_enabled' && (
                        <FormDescription>
                          {t('Only attempt compression when the image exceeds either threshold.')}
                        </FormDescription>
                      )}
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={(checked) => {
                          field.onChange(checked)
                          if (name === 'threshold_enabled') {
                            void form.trigger('max_dimension')
                          }
                        }}
                        disabled={name !== 'enabled' && !enabled}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />
            ))}
            {remote && (
              <Alert>
                <AlertDescription>
                  {t(
                    'Remote downloads require HTTPS and an allowed domain in SSRF settings.'
                  )}{' '}
                  <Link
                    to='/system-settings/security/$section'
                    params={{ section: 'ssrf' }}
                    className='underline underline-offset-4'
                  >
                    {t('SSRF settings')}
                  </Link>
                </AlertDescription>
              </Alert>
            )}
          </SettingsControlGroup>
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={!enabled}
              onClick={() => preset({})}
            >
              {t('Balanced')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={!enabled}
              onClick={() =>
                preset({
                  min_bytes_kb: 128,
                  min_dimension: 1024,
                  max_dimension: 1024,
                  jpeg_quality: 62,
                  resampler: 'bilinear',
                })
              }
            >
              {t('Smaller files')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={!enabled}
              onClick={() =>
                preset({
                  min_bytes_kb: 1024,
                  min_dimension: 4096,
                  max_dimension: 4096,
                  jpeg_quality: 92,
                })
              }
            >
              {t('Higher quality')}
            </Button>
          </div>
          <SettingsControlGroup className='grid gap-4 sm:grid-cols-2'>
            {fields.map(([name, label]) => (
              <FormField
                key={name}
                control={form.control}
                name={name}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {name === 'jpeg_quality' ? (
                        <span id={qualityLabelId}>{label}</span>
                      ) : (
                        label
                      )}
                    </FormLabel>
                    <div className='flex items-center gap-3'>
                      {name === 'jpeg_quality' && (
                        <Slider
                          aria-labelledby={qualityLabelId}
                          min={30}
                          max={95}
                          step={1}
                          value={[
                            Number.isNaN(field.value)
                              ? imageCompressionDefaults.jpeg_quality
                              : Math.max(
                                  30,
                                  Math.min(95, Math.round(field.value))
                                ),
                          ]}
                          onValueChange={(value) =>
                            field.onChange(
                              typeof value === 'number' ? value : value[0]
                            )
                          }
                          disabled={!enabled || format === 'png'}
                        />
                      )}
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          value={Number.isNaN(field.value) ? '' : field.value}
                          onChange={(event) =>
                            field.onChange(event.target.valueAsNumber)
                          }
                          disabled={
                            !enabled ||
                            (name === 'jpeg_quality' && format === 'png') ||
                            ((name === 'min_bytes_kb' || name === 'min_dimension') && !thresholdEnabled)
                          }
                          className={
                            name === 'jpeg_quality'
                              ? 'w-20 shrink-0'
                              : undefined
                          }
                        />
                      </FormControl>
                    </div>
                    {name === 'max_dimension' && (
                      <div className='flex flex-wrap gap-2'>
                        {[1024, 1536, 2048, 4096].map((edge) => (
                          <Button
                            key={edge}
                            type='button'
                            variant='outline'
                            size='xs'
                            disabled={!enabled}
                            aria-label={t(
                              'Set target edge to {{edge}} pixels',
                              { edge }
                            )}
                            onClick={() =>
                              form.setValue(name, edge, {
                                shouldDirty: true,
                                shouldValidate: true,
                              })
                            }
                          >
                            {edge}
                          </Button>
                        ))}
                      </div>
                    )}
                    <FormMessage />
                  </FormItem>
                )}
              />
            ))}
            {(
              [
                [
                  'target_format',
                  t('Output format'),
                  [
                    ['auto_keep_alpha', t('Automatic with transparency')],
                    ['jpeg', 'JPEG'],
                    ['png', 'PNG'],
                    ['keep_original', t('Keep original format')],
                  ],
                ],
                [
                  'resampler',
                  t('Scaling filter'),
                  [
                    ['catmull_rom', 'Catmull–Rom'],
                    ['bilinear', t('Bilinear')],
                  ],
                ],
                [
                  'failure_policy',
                  t('When compression fails'),
                  [
                    ['keep_original', t('Keep original image')],
                    ['reject_request', t('Reject request')],
                  ],
                ],
              ] as const
            ).map(([name, label, options]) => (
              <FormField
                key={name}
                control={form.control}
                name={name}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{label}</FormLabel>
                    <FormControl>
                      <NativeSelect {...field} disabled={!enabled}>
                        {options.map(([value, caption]) => (
                          <NativeSelectOption key={value} value={value}>
                            {caption}
                          </NativeSelectOption>
                        ))}
                      </NativeSelect>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            ))}
            <ImageCompressionScope disabled={!enabled} />
          </SettingsControlGroup>
          <ImageCompressionPreview
            withDraft={(run) => form.handleSubmit(run)()}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
