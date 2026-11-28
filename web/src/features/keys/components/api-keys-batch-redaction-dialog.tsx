import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { batchUpdateApiKeyRedaction } from '../api'
import { redactionErrorMessage } from '../lib/redaction-feedback'
import type { ApiKey } from '../types'
import { useApiKeys } from './api-keys-provider'

type BatchResult = NonNullable<Awaited<ReturnType<typeof batchUpdateApiKeyRedaction>>['data']>

export function ApiKeysBatchRedactionDialog(props: {
  keys: ApiKey[]
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { triggerRefresh } = useApiKeys()
  const [pending, setPending] = useState(false)
  const [result, setResult] = useState<BatchResult>()
  const unavailable = !props.keys.length || props.keys.length > 100 || props.keys.some((key) => key.redaction_revision === undefined)

  async function update(enabled: boolean) {
    if (unavailable || pending || result) return
    setPending(true)
    try {
      const items = props.keys.flatMap((key) => key.redaction_revision === undefined ? [] : [{
        id: key.id,
        redaction_revision: key.redaction_revision,
        redaction_enabled: enabled,
      }])
      const response = requireServerSuccess(await batchUpdateApiKeyRedaction(items))
      if (!response.data) throw new Error(t('Could not update redaction'))
      setResult(response.data)
      triggerRefresh()
    } catch (error) {
      handleServerError(error, t('Could not update redaction'))
    } finally {
      setPending(false)
    }
  }

  const results = new Map(result?.items.map((item) => [item.id, item]))
  return (
    <Dialog
      open
      onOpenChange={(open) => { if (!open && !pending) props.onClose() }}
      title={t('Configure selected keys’ redaction')}
      description={t('Existing rules and scopes are kept.')}
      showCloseButton={!pending}
      footer={result ? (
        <Button type='button' onClick={props.onClose}>{t('Close')}</Button>
      ) : (
        <>
          <Button type='button' variant='outline' disabled={unavailable || pending} onClick={() => void update(false)}>{t('Disable redaction')}</Button>
          <Button type='button' disabled={unavailable || pending} onClick={() => void update(true)}>{t('Enable redaction')}</Button>
        </>
      )}
    >
      <div aria-live='polite' aria-busy={pending}>
        {unavailable && <p>{t('Select up to 100 keys with a loaded redaction policy version.')}</p>}
        {pending && <p>{t('Saving...')}</p>}
        {result && <p>{t('Updated {{count}} API key(s)', { count: result.updated })}</p>}
        <ul>
          {props.keys.map((key) => {
            const item = results.get(key.id)
            let detail: string | undefined
            if (item?.success) detail = t('Success')
            else if (item?.code === 'redaction_revision_conflict') detail = t('Policy changed. Refresh and try again.')
            else if (item) detail = redactionErrorMessage(t, item, t('Could not update redaction'))
            else if (result) detail = t('Could not update redaction')
            return <li key={key.id}>{key.name}{detail && <> · {detail}</>}</li>
          })}
        </ul>
      </div>
    </Dialog>
  )
}
