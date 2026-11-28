import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  cacheKey,
  clearToolCache,
  getCacheStatus,
  type CacheSelection,
} from '../lib/admin'

export function ToolsCache() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const id = useId()
  const client = useQueryClient()
  const status = useQuery({ queryKey: cacheKey, queryFn: getCacheStatus })
  const [scope, setScope] = useState<CacheSelection['scope']>('forward')
  const [kind, setKind] = useState<'all' | 'query' | 'url'>('all')
  const [selection, setSelection] = useState<CacheSelection>()
  const clear = useMutation({
    mutationFn: clearToolCache,
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: cacheKey })
      toast.success(t('Tool cache cleared'))
      setSelection(undefined)
    },
    onError: (error) =>
      handleServerError(error, t('Could not clear tool cache')),
  })
  const scopeLabel = (name: string) =>
    name === 'forward' ? t('Forward cache') : t('Redirect cache')

  return (
    <Card>
      <CardHeader>
        <CardTitle id={`${id}-title`}>{t('Tool cache')}</CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        {status.isPending && <p role='status'>{t('Loading...')}</p>}
        {status.isError && (
          <Button
            type='button'
            variant='outline'
            className='self-start'
            onClick={() => void status.refetch()}
          >
            {t('Retry loading cache status')}
          </Button>
        )}
        {!!status.data?.length && (
          <ul className='flex flex-col gap-1 text-sm'>
            {status.data.map((item) => (
              <li key={item.scope}>
                {scopeLabel(item.scope)}: {item.backend} ·{' '}
                {item.backend === 'redis'
                  ? t('Shared across instances')
                  : t('This instance only')}
                <dl className='text-muted-foreground'>
                  {item.valid_entries != null && (
                    <div>
                      <dt className='inline'>{t('Valid entries')}: </dt>
                      <dd className='inline'>
                        {item.entries_truncated
                          ? t('At least {{entries}} (scan limit reached)', {
                              entries: formatNumber(item.valid_entries, locale),
                            })
                          : formatNumber(item.valid_entries, locale)}
                      </dd>
                    </div>
                  )}
                  <div>
                    <dt className='inline'>{t('Exact invalidation markers')}: </dt>
                    <dd className='inline'>
                      {formatNumber(item.exact_invalidations, locale)}
                    </dd>
                  </div>
                  {item.generation != null && (
                    <div>
                      <dt className='inline'>{t('Cache generation')}: </dt>
                      <dd className='inline'>
                        <code className='wrap-anywhere'>
                          {item.generation || '—'}
                        </code>
                      </dd>
                    </div>
                  )}
                </dl>
              </li>
            ))}
          </ul>
        )}
        <form
          aria-labelledby={`${id}-title`}
          method='post'
          onSubmit={(event) => {
            event.preventDefault()
            const target = String(
              new FormData(event.currentTarget).get('target') ?? ''
            ).trim()
            if (kind !== 'all' && !target) return
            clear.reset()
            setSelection({
              scope,
              ...(kind === 'all' ? {} : { [kind]: target }),
            })
          }}
        >
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor={`${id}-scope`}>
                {t('Cache scope')}
              </FieldLabel>
              <NativeSelect
                id={`${id}-scope`}
                name='scope'
                value={scope}
                onChange={(event) => {
                  const next = event.target.value as CacheSelection['scope']
                  setScope(next)
                  if (next === 'redirect' && kind === 'query') setKind('all')
                }}
              >
                <NativeSelectOption value='forward'>
                  {t('Forward cache')}
                </NativeSelectOption>
                <NativeSelectOption value='redirect'>
                  {t('Redirect cache')}
                </NativeSelectOption>
              </NativeSelect>
            </Field>
            <Field>
              <FieldLabel htmlFor={`${id}-kind`}>{t('Clear by')}</FieldLabel>
              <NativeSelect
                id={`${id}-kind`}
                name='kind'
                value={kind}
                onChange={(event) => setKind(event.target.value as typeof kind)}
              >
                <NativeSelectOption value='all'>
                  {t('Entire cache')}
                </NativeSelectOption>
                {scope === 'forward' && (
                  <NativeSelectOption value='query'>
                    {t('Exact query')}
                  </NativeSelectOption>
                )}
                <NativeSelectOption value='url'>
                  {t('Exact URL')}
                </NativeSelectOption>
              </NativeSelect>
            </Field>
            {kind !== 'all' && (
              <Field>
                <FieldLabel htmlFor={`${id}-target`}>
                  {kind === 'query' ? t('Exact query') : t('Exact URL')}
                </FieldLabel>
                <Input
                  key={kind}
                  id={`${id}-target`}
                  name='target'
                  type={kind === 'url' ? 'url' : 'text'}
                  maxLength={8192}
                  required
                />
              </Field>
            )}
            <Button
              type='submit'
              variant='outline'
              className='self-start'
              disabled={!status.data || clear.isPending}
            >
              {t('Clear cache')}
            </Button>
          </FieldGroup>
        </form>
        <ConfirmDialog
          open={!!selection}
          onOpenChange={(open) => {
            if (!open && !clear.isPending) setSelection(undefined)
          }}
          title={t('Clear tool cache?')}
          desc={
            selection?.query || selection?.url
              ? t('Only matching entries in {{scope}} will be invalidated.', {
                  scope: scopeLabel(selection.scope),
                })
              : t('All entries in {{scope}} will be invalidated.', {
                  scope: scopeLabel(selection?.scope ?? scope),
                })
          }
          confirmText={t('Clear cache')}
          destructive
          isLoading={clear.isPending}
          handleConfirm={() => {
            if (selection) clear.mutate(selection)
          }}
        />
      </CardContent>
    </Card>
  )
}
