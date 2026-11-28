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
  SettingsSwitchContent,
  SettingsSwitchItem,
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

  const watchedMaxEdge = useWatch({ control: form.control, name: 'max_dimension' })
  const watchedQuality = useWatch({ control: form.control, name: 'jpeg_quality' })
  const watchedMinKb = useWatch({ control: form.control, name: 'min_bytes_kb' })
  const watchedResampler = useWatch({ control: form.control, name: 'resampler' })

  let activePreset: 'smaller' | 'quality' | 'balanced' | null = null
  if (
    watchedMaxEdge === 1024 &&
    watchedQuality === 62 &&
    watchedMinKb === 128 &&
    watchedResampler === 'bilinear'
  ) {
    activePreset = 'smaller'
  } else if (
    watchedMaxEdge === 4096 &&
    watchedQuality === 92 &&
    watchedMinKb === 1024
  ) {
    activePreset = 'quality'
  } else if (
    watchedMaxEdge === imageCompressionDefaults.max_dimension &&
    watchedQuality === imageCompressionDefaults.jpeg_quality &&
    watchedMinKb === imageCompressionDefaults.min_bytes_kb
  ) {
    activePreset = 'balanced'
  }

  const thresholdFields = [
    ['min_bytes_kb', t('Size threshold (KB)')],
    ['min_dimension', t('Dimension threshold (px)')],
  ] as const

  const budgetFields = [
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

          <SettingsControlGroup className='space-y-4'>
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Enable image compression')}</FormLabel>
                    <FormDescription>
                      {t('Compress oversized images before forwarding requests upstream.')}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <div className='grid gap-2 sm:grid-cols-3'>
              <Button
                type='button'
                variant={activePreset === 'balanced' ? 'secondary' : 'outline'}
                className='h-auto flex-col items-start gap-0.5 px-3 py-2.5 text-left'
                disabled={!enabled}
                onClick={() => preset({})}
              >
                <span className='text-sm font-medium'>{t('Balanced')}</span>
                <span className='text-muted-foreground text-xs font-normal'>
                  2048px · Q82 · Catmull–Rom
                </span>
              </Button>
              <Button
                type='button'
                variant={activePreset === 'smaller' ? 'secondary' : 'outline'}
                className='h-auto flex-col items-start gap-0.5 px-3 py-2.5 text-left'
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
                <span className='text-sm font-medium'>{t('Smaller files')}</span>
                <span className='text-muted-foreground text-xs font-normal'>
                  1024px · Q62 · Bilinear
                </span>
              </Button>
              <Button
                type='button'
                variant={activePreset === 'quality' ? 'secondary' : 'outline'}
                className='h-auto flex-col items-start gap-0.5 px-3 py-2.5 text-left'
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
                <span className='text-sm font-medium'>{t('Higher quality')}</span>
                <span className='text-muted-foreground text-xs font-normal'>
                  4096px · Q92 · Catmull–Rom
                </span>
              </Button>
            </div>
          </SettingsControlGroup>

          <SettingsControlGroup className='space-y-4'>
            <FormField
              control={form.control}
              name='threshold_enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Use compression thresholds')}</FormLabel>
                    <FormDescription>
                      {t('Only attempt compression when the image exceeds either threshold.')}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={(checked) => {
                        field.onChange(checked)
                        void form.trigger('max_dimension')
                      }}
                      disabled={!enabled}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <div className='grid gap-4 sm:grid-cols-2'>
              {thresholdFields.map(([name, label]) => (
                <FormField
                  key={name}
                  control={form.control}
                  name={name}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{label}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          value={Number.isNaN(field.value) ? '' : field.value}
                          onChange={(event) =>
                            field.onChange(event.target.valueAsNumber)
                          }
                          disabled={!enabled || !thresholdEnabled}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ))}
            </div>
            <div className='grid gap-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='max_dimension'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Target max edge (px)')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='number'
                        value={Number.isNaN(field.value) ? '' : field.value}
                        onChange={(event) =>
                          field.onChange(event.target.valueAsNumber)
                        }
                        disabled={!enabled}
                      />
                    </FormControl>
                    <div className='flex flex-wrap gap-1.5'>
                      {[1024, 1536, 2048, 4096].map((edge) => (
                        <Button
                          key={edge}
                          type='button'
                          variant={field.value === edge ? 'secondary' : 'outline'}
                          size='xs'
                          disabled={!enabled}
                          aria-label={t('Set target edge to {{edge}} pixels', {
                            edge,
                          })}
                          onClick={() =>
                            form.setValue('max_dimension', edge, {
                              shouldDirty: true,
                              shouldValidate: true,
                            })
                          }
                        >
                          {edge}
                        </Button>
                      ))}
                    </div>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='jpeg_quality'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      <span id={qualityLabelId}>{t('JPEG quality')}</span>
                    </FormLabel>
                    <div className='flex items-center gap-3'>
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
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          value={Number.isNaN(field.value) ? '' : field.value}
                          onChange={(event) =>
                            field.onChange(event.target.valueAsNumber)
                          }
                          disabled={!enabled || format === 'png'}
                          className='w-20 shrink-0'
                        />
                      </FormControl>
                    </div>
                    <FormMessage />
                  </FormItem>
                )}
              />
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
            </div>
          </SettingsControlGroup>

          <SettingsControlGroup className='space-y-4'>
            <div className='grid gap-3'>
              <FormField
                control={form.control}
                name='skip_animated_and_svg'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>
                        {t('Preserve animated images and SVG')}
                      </FormLabel>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={!enabled}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
              <FormField
                control={form.control}
                name='compress_remote_urls'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>
                        {t('Download and compress remote images')}
                      </FormLabel>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={!enabled}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
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
            </div>
            <div className='grid gap-4 sm:grid-cols-2'>
              {budgetFields.map(([name, label]) => (
                <FormField
                  key={name}
                  control={form.control}
                  name={name}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{label}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          value={Number.isNaN(field.value) ? '' : field.value}
                          onChange={(event) =>
                            field.onChange(event.target.valueAsNumber)
                          }
                          disabled={!enabled}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ))}
              <FormField
                control={form.control}
                name='failure_policy'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('When compression fails')}</FormLabel>
                    <FormControl>
                      <NativeSelect {...field} disabled={!enabled}>
                        <NativeSelectOption value='keep_original'>
                          {t('Keep original image')}
                        </NativeSelectOption>
                        <NativeSelectOption value='reject_request'>
                          {t('Reject request')}
                        </NativeSelectOption>
                      </NativeSelect>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
            <div className='grid gap-4 border-t pt-4 sm:grid-cols-2'>
              <ImageCompressionScope disabled={!enabled} />
            </div>
          </SettingsControlGroup>
          <ImageCompressionPreview
            withDraft={(run) => form.handleSubmit(run)()}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
