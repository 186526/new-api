import { fetchTokenKey } from '@/features/keys/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export type SystemOnePreset = {
  id: string
  label: string
  state: string
  questions: string
}

export const SYSTEM_ONE_PRESETS: SystemOnePreset[] = [
  {
    id: 'choice-score',
    label: 'Choice + Score',
    state:
      'A queue drops messages when the consumer is slower than the producer.',
    questions: JSON.stringify(
      {
        verdict: {
          type: 'choice',
          instructions: 'Is the queue lossy?',
          criteria: {
            lossy: 'Messages can be lost.',
            reliable: 'No message is lost.',
          },
        },
        confidence: {
          type: 'score',
          instructions: 'How sure are you about the verdict?',
          criteria: ['unsure', 'fairly sure', 'certain'],
        },
      },
      null,
      2
    ),
  },
  {
    id: 'noul',
    label: 'Boolean (noul)',
    state:
      'The user wrote: "Please cancel my subscription immediately and refund this month."',
    questions: JSON.stringify(
      {
        wants_cancellation: {
          type: 'noul',
          instructions: 'Is the user asking to cancel their subscription?',
        },
        wants_refund: {
          type: 'noul',
          instructions: 'Is the user requesting a refund?',
        },
      },
      null,
      2
    ),
  },
]

export type SystemOneRunResult = {
  ok: boolean
  status: number
  body: unknown
  rawText: string
}

export async function listSystemOneModels(
  keyId: number,
  signal: AbortSignal
): Promise<string[]> {
  const key = requireServerSuccess(await fetchTokenKey(keyId)).data?.key
  if (!key) return ['jev-latest']
  const response = await fetch('/systemone/v1/models', {
    headers: { Authorization: `Bearer sk-${key}` },
    signal,
  })
  if (!response.ok) return ['jev-latest']
  const payload = (await response.json()) as {
    data?: Array<{ id?: string }>
  }
  const ids = (payload.data ?? [])
    .map((item) => item.id?.trim())
    .filter((id): id is string => Boolean(id))
  return ids.length > 0 ? ids : ['jev-latest']
}

export async function runSystemOneRequest(
  params: {
    keyId: number
    model: string
    state: unknown
    questions: Record<string, unknown>
    longContext?: boolean
    images?: boolean
  },
  signal: AbortSignal
): Promise<SystemOneRunResult> {
  const key = requireServerSuccess(await fetchTokenKey(params.keyId)).data?.key
  if (!key) throw new Error('Could not load API key')
  const headers: Record<string, string> = {
    Authorization: `Bearer sk-${key}`,
    'Content-Type': 'application/json',
  }
  if (params.longContext) headers['X-SystemOne-Long-Context'] = 'true'
  if (params.images) headers['X-SystemOne-Images'] = 'true'

  const response = await fetch('/systemone/v1/systemone', {
    method: 'POST',
    headers,
    body: JSON.stringify({
      model: params.model,
      state: params.state,
      questions: params.questions,
    }),
    signal,
  })
  const rawText = await response.text()
  let body: unknown = rawText
  try {
    body = JSON.parse(rawText)
  } catch {
    // Keep raw text when response is not JSON.
  }
  return {
    ok: response.ok,
    status: response.status,
    body,
    rawText,
  }
}
