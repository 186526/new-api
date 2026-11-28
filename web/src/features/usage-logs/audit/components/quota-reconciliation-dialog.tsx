import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import type { ApiResponse } from '@/features/profile/types'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

const endpoint = '/api/audit/quota-reconciliation'
const identitySchema = z
  .object({
    request_id: z.string().trim().max(64),
    region: z.string().trim().max(64),
    subject_id: z.string().trim().max(20),
    sequence: z.string().trim().max(19),
  })
  .refine((identity) => {
    if (identity.request_id) {
      return !identity.region && !identity.subject_id && !identity.sequence
    }
    return (
      !!identity.region &&
      /^[1-9][0-9]*$/.test(identity.subject_id) &&
      /^[1-9][0-9]*$/.test(identity.sequence)
    )
  })
const reasonSchema = z.object({ reason: z.string().trim().min(1).max(2000) })
type Identity = z.infer<typeof identitySchema>
type Review = {
  receipts?: {
    part: string
    status: string
    subject_id: number
    quota: number
    restored: number
    restore_reason?: string
    version: number
    settlement_quota: number
    settlement_delta: number
    uncollected_quota: number
  }[]
  region_event?: { delta: number }
  confirmable: boolean
  evidence_hash?: string
  confirmation?: { reason: string; operator_id: number }
}

function identityParams(identity: Identity) {
  if (identity.request_id) return { request_id: identity.request_id }
  return {
    region: identity.region,
    subject_type: 'user',
    subject_id: identity.subject_id,
    sequence: identity.sequence,
  }
}

function QuotaReviewForm(props: { canConfirm: boolean }) {
  const { t } = useTranslation()
  const id = useId()
  const [result, setResult] = useState<{ identity: Identity; review: Review }>()
  const lookupForm = useForm<Identity>({
    resolver: zodResolver(identitySchema),
    defaultValues: { request_id: '', region: '', subject_id: '', sequence: '' },
  })
  const reasonForm = useForm<z.infer<typeof reasonSchema>>({
    resolver: zodResolver(reasonSchema),
    defaultValues: { reason: '' },
  })
  const lookup = useMutation({
    mutationFn: async (identity: Identity) => {
      const response = await api.get<ApiResponse<Review>>(endpoint, {
        params: identityParams(identity),
      })
      const review = requireServerSuccess(response.data).data
      if (!review) throw new Error(t('Quota review unavailable'))
      return { identity, review }
    },
    onSuccess: setResult,
    retry: false,
  })
  const confirm = useMutation({
    mutationFn: async (submission: {
      identity: Identity
      evidenceHash: string
      reason: string
    }) => {
      const response = await api.post<ApiResponse<null>>(endpoint, {
        ...identityParams(submission.identity),
        evidence_hash: submission.evidenceHash,
        reason: submission.reason,
      })
      requireServerSuccess(response.data)
    },
    onSuccess: (_, submission) => lookup.mutate(submission.identity),
    onError: (error) => {
      setResult(undefined)
      handleServerError(error)
    },
    retry: false,
  })
  const busy = lookup.isPending || confirm.isPending
  const review = result?.review
  let reviewStatus: string | undefined
  if (review?.confirmation) {
    reviewStatus = t('Evidence confirmed')
  } else if (review?.confirmable) {
    reviewStatus = t('Evidence available')
  } else if (review) {
    reviewStatus = t('Pending review')
  }
  return (
    <div className='flex flex-col gap-4'>
      <form
        aria-label={t('Find quota evidence')}
        onSubmit={lookupForm.handleSubmit((identity) => {
          if (busy) return
          setResult(undefined)
          reasonForm.reset()
          lookup.mutate(identity)
        })}
      >
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor={`${id}-request`}>{t('Request ID')}</FieldLabel>
            <Input
              id={`${id}-request`}
              maxLength={64}
              disabled={busy}
              {...lookupForm.register('request_id')}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-region`}>{t('Region')}</FieldLabel>
            <Input
              id={`${id}-region`}
              maxLength={64}
              disabled={busy}
              {...lookupForm.register('region')}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-subject`}>{t('User ID')}</FieldLabel>
            <Input
              id={`${id}-subject`}
              inputMode='numeric'
              maxLength={20}
              disabled={busy}
              {...lookupForm.register('subject_id')}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-sequence`}>
              {t('Report sequence')}
            </FieldLabel>
            <Input
              id={`${id}-sequence`}
              inputMode='numeric'
              maxLength={19}
              disabled={busy}
              {...lookupForm.register('sequence')}
            />
          </Field>
          {Object.keys(lookupForm.formState.errors).length > 0 && (
            <p role='alert'>
              {t('Enter a request ID or all three regional report fields.')}
            </p>
          )}
          <Button type='submit' disabled={busy}>
            {t('Find evidence')}
          </Button>
        </FieldGroup>
      </form>
      {review && <p role='status'>{reviewStatus}</p>}
      {review?.region_event && (
        <p>{t('Original report queued; application is not confirmed.')}</p>
      )}
      {review?.receipts && (
        <ul>
          {review.receipts.map((receipt) => (
            <li key={receipt.part}>
              {t(
                'Receipt {{part}}: {{status}}; user or token {{subject}}, quota {{quota}}, restored {{restored}}, settled {{settlement_quota}}, settlement change {{settlement_delta}}, uncollected {{uncollected_quota}}, version {{version}}',
                {
                  ...receipt,
                  subject: receipt.subject_id,
                }
              )}
              {receipt.restore_reason && ` (${receipt.restore_reason})`}
            </li>
          ))}
        </ul>
      )}
      {review?.confirmation && (
        <p className='wrap-break-word'>
          {t('Operator ID')}: {review.confirmation.operator_id} —{' '}
          {review.confirmation.reason}
        </p>
      )}
      {props.canConfirm &&
        result &&
        review?.confirmable &&
        review.evidence_hash && (
          <form
            aria-label={t('Confirm evidence')}
            onSubmit={reasonForm.handleSubmit(({ reason }) => {
              if (!busy && review.evidence_hash) {
                confirm.mutate({
                  identity: result.identity,
                  evidenceHash: review.evidence_hash,
                  reason,
                })
              }
            })}
          >
            <FieldGroup>
              <Field data-invalid={!!reasonForm.formState.errors.reason}>
                <FieldLabel htmlFor={`${id}-reason`}>
                  {t('Confirmation reason')}
                </FieldLabel>
                <Textarea
                  id={`${id}-reason`}
                  required
                  maxLength={2000}
                  disabled={busy}
                  aria-invalid={!!reasonForm.formState.errors.reason}
                  {...reasonForm.register('reason')}
                />
              </Field>
              <Button type='submit' disabled={busy}>
                {t('Confirm evidence')}
              </Button>
            </FieldGroup>
          </form>
        )}
    </div>
  )
}

export function QuotaReconciliationDialog(props: { canConfirm: boolean }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Quota reconciliation')}
      trigger={<Button variant='outline'>{t('Review quota')}</Button>}
    >
      {open && <QuotaReviewForm canConfirm={props.canConfirm} />}
    </Dialog>
  )
}
