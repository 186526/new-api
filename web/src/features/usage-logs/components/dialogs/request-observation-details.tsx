import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { LogOtherData } from '../../types'
import { DetailRow, DetailSection } from './log-detail-layout'

export function RequestObservationDetails(props: {
  other?: LogOtherData | null
  isAdmin: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const other = props.other
  const tool = other?.tool_execution
  const image = other?.image_compression
  const redaction = other?.request_redaction
  const reasoning = other?.automatic_reasoning
  if (!tool && !image && !redaction && !reasoning) return null

  const provider = props.isAdmin
    ? other?.admin_info?.tool_execution?.provider
    : undefined
  const outcomes = {
    success: t('Success'),
    empty: t('No results'),
    upstream_error: t('Upstream error'),
    unavailable: t('Unavailable'),
  }
  const refunds: Partial<Record<string, string>> = {
    not_needed: t('Refund not needed'),
    requested: t('Refund requested'),
    completed: t('Refund completed'),
    failed: t('Refund failed'),
  }
  const imageReasons: Partial<Record<string, string>> = {
    passthrough: t('Request body passthrough'),
    excluded: t('Excluded by policy'),
    unsupported: t('Unsupported image'),
    budget: t('Compression budget exceeded'),
    failed: t('Compression failed'),
    retained: t('Original images retained'),
    compressed: t('Images compressed'),
  }
  const outboundStatuses = {
    not_replayable: t('Outbound body cannot be inspected'),
    body_too_large: t('Outbound body exceeds inspection limit'),
    read_failed: t('Could not inspect outbound body'),
    invalid_json: t('Outbound body is not JSON'),
    not_observed: t('Outbound reasoning fields not confirmed'),
  }
  const reasoningDecisions: Partial<Record<string, string>> = {
    applied: t('Policy applied'),
    passthrough: t('Request body passthrough'),
    unsupported_model: t('Unsupported model'),
    blacklisted: t('Model exempt from automatic reasoning'),
    model_modifier: t('Model suffix takes priority'),
    group_excluded: t('Group excluded by policy'),
    model_excluded: t('Model excluded by policy'),
    explicit_disable: t('Reasoning explicitly disabled'),
    explicit_strength: t('Explicit effort retained'),
    within_cap: t('Explicit effort within policy cap'),
  }
  const outboundFields = Object.entries(reasoning?.outbound_fields ?? {})
    .map(([field, value]) => `${field}: ${String(value)}`)
    .join(', ')
  const emptyOutboundFields =
    reasoning?.outbound_status === 'observed'
      ? t('No outbound reasoning fields returned.')
      : t('Outbound reasoning fields not confirmed')
  const outboundDetails =
    observationLabel(outboundStatuses, reasoning?.outbound_status) ??
    (outboundFields || emptyOutboundFields)
  const outcome = observationLabel(outcomes, tool?.outcome)
  const refund = observationLabel(refunds, tool?.refund)
  const imageReason = observationLabel(imageReasons, image?.reason)
  const imageSkipped =
    image?.reason === 'passthrough' ||
    image?.reason === 'excluded' ||
    image?.reason === 'unsupported' ||
    image?.reason === 'retained'
  const missingImageValue = imageSkipped ? t('Skipped') : t('Unavailable')
  const compressionWork =
    image?.preprocessing ?? (imageSkipped ? undefined : image)
  const reasoningDecision = observationLabel(
    reasoningDecisions,
    reasoning?.reason
  )

  return (
    <DetailSection label={t('Request observations')}>
      {tool && (
        <>
          <DetailRow label={t('Tool API')} value={tool.contract} mono />
          {provider && <DetailRow label={t('Provider')} value={provider} />}
          {outcome && <DetailRow label={t('Result')} value={outcome} />}
          <DetailRow
            label={t('Cache hit')}
            value={tool.cache_hit ? t('Yes') : t('No')}
          />
          {refund && <DetailRow label={t('Refund status')} value={refund} />}
        </>
      )}
      {image && (
        <>
          <DetailRow
            label={t('Images compressed')}
            value={
              image.outbound_compressed == null
                ? missingImageValue
                : formatNumber(image.outbound_compressed, locale)
            }
          />
          <DetailRow
            label={t('Image payload bytes saved')}
            value={
              image.outbound_saved_bytes == null
                ? missingImageValue
                : formatNumber(image.outbound_saved_bytes, locale)
            }
          />
          {compressionWork && (
            <>
              <DetailRow
                label={t('Images preprocessed')}
                value={formatNumber(compressionWork.processed, locale)}
              />
              <DetailRow
                label={t('Images skipped')}
                value={formatNumber(compressionWork.skipped, locale)}
              />
              <DetailRow
                label={t('Image compression failures')}
                value={formatNumber(compressionWork.failed, locale)}
              />
              <DetailRow
                label={t('Image compression time (ms)')}
                value={formatNumber(compressionWork.elapsed_ms, locale)}
              />
            </>
          )}
          {imageReason && (
            <DetailRow
              label={t('Image compression reason')}
              value={imageReason}
            />
          )}
        </>
      )}
      {redaction && (
        <>
          <DetailRow
            label={t('Redaction matches')}
            value={formatNumber(redaction.matches, locale)}
          />
          <DetailRow
            label={t('Redaction categories')}
            value={formatNumber(redaction.categories, locale)}
          />
        </>
      )}
      {reasoning && (
        <>
          <DetailRow
            label={t('Automatic reasoning')}
            value={reasoningDecision ?? t('Skipped')}
          />
          <DetailRow
            label={t('Reasoning input scale')}
            value={formatNumber(reasoning.scale, locale)}
          />
          {reasoning.reason !== 'explicit_disable' &&
            (reasoning.policy_effort || reasoning.effort) && (
              <DetailRow
                label={t('Reasoning policy effort')}
                value={reasoning.policy_effort || reasoning.effort}
              />
            )}
          {reasoning.conflict_resolution &&
            reasoning.conflict_resolution !== 'applied' && (
              <DetailRow
                label={t('Reasoning conflict resolution')}
                value={t(
                  `reasoning.preview.reason.${reasoning.conflict_resolution}`,
                  {
                    defaultValue: reasoning.conflict_resolution,
                  }
                )}
              />
            )}
          {reasoning.tier !== undefined && (
            <DetailRow
              label={t('Matched tier')}
              value={
                reasoning.tier > 0
                  ? formatNumber(reasoning.tier, locale)
                  : t('Overflow effort')
              }
            />
          )}
          {reasoning.outbound_fields || reasoning.outbound_status ? (
            <DetailRow
              label={t('Final outbound reasoning fields')}
              value={outboundDetails}
              mono
            />
          ) : null}
        </>
      )}
    </DetailSection>
  )
}

function observationLabel(
  labels: Partial<Record<string, string>>,
  value?: string
): string | undefined {
  return value && Object.hasOwn(labels, value) ? labels[value] : undefined
}
