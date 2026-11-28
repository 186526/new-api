import { useMutation } from '@tanstack/react-query'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { ImageCompressionSettings } from './image-compression-settings'

type ImageInfo = {
  width: number
  height: number
  bytes: number
  format: string
}
type Comparison = {
  original: ImageInfo
  result: ImageInfo
  saved_bytes: number
  elapsed_ms: number
  outcome: string
  image_base64?: string
  image_mime?: string
}
type Response = { success: boolean; message?: string; data: Comparison }
type Props = {
  withDraft: (
    run: (settings: ImageCompressionSettings) => void
  ) => Promise<void>
}

const imageTypes = ['image/png', 'image/jpeg', 'image/webp']

function PreviewImages(props: {
  file: File
  data: Comparison
  dimensions: (info: ImageInfo) => string
}) {
  const { t } = useTranslation()
  const { file, data } = props
  const [urls, setUrls] = useState<{ original: string; result?: string }>()
  useEffect(() => {
    const original = URL.createObjectURL(file)
    let encoded: string | undefined
    if (
      data.outcome === 'compressed' &&
      data.image_base64 &&
      data.image_mime &&
      imageTypes.includes(data.image_mime)
    ) {
      try {
        const bytes = Uint8Array.from(atob(data.image_base64), (byte) =>
          byte.charCodeAt(0)
        )
        encoded = URL.createObjectURL(
          new Blob([bytes], { type: data.image_mime })
        )
      } catch {
        // Keep the comparison statistics if the response cannot be decoded.
      }
    }
    setUrls({
      original,
      result: data.outcome === 'compressed' ? encoded : original,
    })
    return () => {
      URL.revokeObjectURL(original)
      if (encoded) URL.revokeObjectURL(encoded)
    }
  }, [file, data])

  const figures = [
    { label: t('Original'), info: data.original, src: urls?.original },
    { label: t('Result'), info: data.result, src: urls?.result },
  ]
  return figures.map(({ label, info, src }) => (
    <figure
      key={label}
      className='bg-card min-w-0 overflow-hidden rounded-lg border'
    >
      <div className='bg-muted/40 flex h-48 items-center justify-center sm:h-64'>
        {src ? (
          <img
            src={src}
            alt={label}
            width={info.width || undefined}
            height={info.height || undefined}
            className='h-full w-full object-contain'
          />
        ) : (
          urls && (
            <p className='text-muted-foreground px-3 text-center text-sm'>
              {t('Preview unavailable')}
            </p>
          )
        )}
      </div>
      <figcaption className='px-3 py-2 text-sm'>
        <span className='font-medium'>{label}</span>
        <p className='text-muted-foreground'>{props.dimensions(info)}</p>
      </figcaption>
    </figure>
  ))
}

function getFileError(file?: File) {
  if (!file) return
  if (file.size === 0 || file.size > 16 * 1024 * 1024) return 'size'
  if (file.type && !imageTypes.includes(file.type)) return 'format'
}

export function ImageCompressionPreview(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const id = useId()
  const [file, setFile] = useState<File>()
  const request = useMutation({
    mutationFn: async ({
      settings,
      file,
    }: {
      settings: ImageCompressionSettings
      file: File
    }) => {
      const body = new FormData()
      body.append('settings', JSON.stringify(settings))
      body.append('image', file)
      const response = await api.post<Response>(
        '/api/system-settings/image-compression/preview',
        body
      )
      return requireServerSuccess(response.data).data
    },
  })
  const data = request.data
  const fileError = getFileError(file)

  function selectFile(nextFile?: File) {
    setFile(nextFile)
    request.reset()
  }

  const dimensions = (info: ImageInfo) =>
    `${formatNumber(info.width, locale)} × ${formatNumber(info.height, locale)} · ${info.format.toUpperCase()} · ${formatNumber(info.bytes, locale)} B`

  return (
    <div className='flex flex-col gap-3'>
      <h3 className='text-sm font-medium'>{t('Preview image compression')}</h3>
      <Field
        className='rounded-lg border border-dashed p-3'
        data-invalid={Boolean(fileError)}
        onDragOver={(event) => event.preventDefault()}
        onDrop={(event) => {
          event.preventDefault()
          if (!request.isPending && event.dataTransfer.files.length === 1) {
            selectFile(event.dataTransfer.files[0])
          }
        }}
      >
        <FieldLabel htmlFor={id}>{t('Test image')}</FieldLabel>
        <FieldDescription id={`${id}-hint`}>
          {t(
            'Select or drop one PNG, JPEG, or WebP up to 16 MB. Preview uses unsaved settings.'
          )}
        </FieldDescription>
        <Input
          id={id}
          name='image'
          type='file'
          accept={imageTypes.join(',')}
          disabled={request.isPending}
          aria-describedby={fileError ? `${id}-hint ${id}-error` : `${id}-hint`}
          aria-invalid={Boolean(fileError)}
          onChange={(event) => selectFile(event.target.files?.[0])}
        />
        <div role='status' className='text-sm break-all'>
          {file && <span>{file.name}</span>}
          {fileError === 'size' && (
            <p id={`${id}-error`}>
              {t('Choose a non-empty image up to 16 MB.')}
            </p>
          )}
          {fileError === 'format' && (
            <p id={`${id}-error`}>{t('Choose a PNG, JPEG, or WebP image.')}</p>
          )}
        </div>
      </Field>
      <Button
        type='button'
        variant='outline'
        className='self-start'
        disabled={!file || Boolean(fileError) || request.isPending}
        onClick={() => {
          if (!file) return
          request.reset()
          void props.withDraft((settings) => request.mutate({ settings, file }))
        }}
      >
        {request.isPending
          ? t('Testing image compression…')
          : t('Test compression')}
      </Button>
      <div role='status'>
        {request.isError && t('Preview failed. Check the image and settings.')}
        {data?.outcome === 'below_threshold' &&
          t('Original kept: below the compression thresholds.')}
        {data?.outcome === 'not_smaller' &&
          t('Original kept: encoding did not reduce the file size.')}
        {data?.outcome === 'preserved_animation_or_svg' &&
          t('Original kept to preserve animation or vector graphics.')}
        {data?.outcome === 'preserved_orientation_metadata' &&
          t('Original kept to preserve image orientation.')}
        {data && file && (
          <div className='my-3 grid gap-3 sm:grid-cols-2'>
            <PreviewImages file={file} data={data} dimensions={dimensions} />
          </div>
        )}
        {data && (
          <dl className='grid gap-3 sm:grid-cols-2'>
            <div>
              <dt>{t('Bytes saved')}</dt>
              <dd>{formatNumber(data.saved_bytes, locale)}</dd>
            </div>
            <div>
              <dt>{t('Processing time (ms)')}</dt>
              <dd>{formatNumber(data.elapsed_ms, locale)}</dd>
            </div>
          </dl>
        )}
      </div>
    </div>
  )
}
