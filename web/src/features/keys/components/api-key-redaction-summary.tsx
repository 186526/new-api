import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import type { ApiKey } from '../types'

export function ApiKeyRedactionSummary(props: { apiKey: ApiKey }) {
  const { t } = useTranslation()
  if (!props.apiKey.redaction_enabled) return null
  const summary = props.apiKey.redaction_summary
  const scopes = summary?.scopes
  const scopeLabels = [
    scopes?.user_messages && t('User messages'),
    scopes?.tool_io && t('Tool inputs and outputs'),
    scopes?.system_prompt && t('System prompt'),
  ].filter(Boolean)
  return (
    <div className='flex flex-wrap items-center gap-1'>
      <Badge variant='secondary'>{t('Redaction enabled')}</Badge>
      {summary && (
        <span className='text-muted-foreground text-xs'>
          {t('Rules: {{count}}', { count: summary.builtin_rules + summary.custom_rules })}
          {scopeLabels.length > 0 && <> · {scopeLabels.join(' / ')}</>}
        </span>
      )}
    </div>
  )
}
