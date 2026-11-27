/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { ApiSampleLanguage } from '../constants'

export type SampleContext = {
  baseUrl: string
  apiKeyEnv: string
  model: string
  /** Endpoint path with `{model}` already replaced. */
  path: string
}

const CHAT_PROMPT = 'Summarize the CAP theorem in two sentences.'

const SYSTEM_ONE_BODY = {
  model: 'jev-latest',
  state:
    'A queue drops messages when the consumer is slower than the producer.',
  questions: {
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
}

function jsonBlock(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

/** Indents a JSON literal so it reads as a continuation of a shell line. */
function indentedJson(value: unknown, prefix: string): string {
  return jsonBlock(value).replaceAll('\n', `\n${prefix}`)
}

function curlRequest(url: string, headers: string[], body?: unknown): string {
  const args = [`curl ${url}`]
  for (const header of headers) {
    args.push(`  -H "${header}"`)
  }
  if (body !== undefined) {
    args.push(`  -d '${indentedJson(body, '     ')}'`)
  }
  return args.join(' \\\n')
}

function bearerHeaders(ctx: SampleContext): string[] {
  return [
    `Authorization: Bearer $${ctx.apiKeyEnv}`,
    'Content-Type: application/json',
  ]
}

// ---------------------------------------------------------------------------
// OpenAI Chat Completions
// ---------------------------------------------------------------------------

function chatSamples(lang: ApiSampleLanguage, ctx: SampleContext): string {
  const body = {
    model: ctx.model,
    messages: [{ role: 'user', content: CHAT_PROMPT }],
  }

  if (lang === 'curl') {
    return curlRequest(`${ctx.baseUrl}${ctx.path}`, bearerHeaders(ctx), body)
  }

  return [
    'import os',
    'from openai import OpenAI',
    '',
    'client = OpenAI(',
    `    base_url="${ctx.baseUrl}/v1",`,
    `    api_key=os.environ["${ctx.apiKeyEnv}"],`,
    ')',
    '',
    'completion = client.chat.completions.create(',
    `    model="${ctx.model}",`,
    `    messages=[{"role": "user", "content": "${CHAT_PROMPT}"}],`,
    ')',
    '',
    'print(completion.choices[0].message.content)',
  ].join('\n')
}

function chatModelsSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  if (lang === 'curl') {
    return curlRequest(`${ctx.baseUrl}${ctx.path}`, [
      `Authorization: Bearer $${ctx.apiKeyEnv}`,
    ])
  }
  return [
    'import os',
    'import requests',
    '',
    'response = requests.get(',
    `    "${ctx.baseUrl}${ctx.path}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    '    timeout=30,',
    ')',
    'print(response.json()["data"][0]["id"])',
  ].join('\n')
}

// ---------------------------------------------------------------------------
// OpenAI Responses
// ---------------------------------------------------------------------------

function responsesSamples(lang: ApiSampleLanguage, ctx: SampleContext): string {
  const body = { model: ctx.model, input: CHAT_PROMPT }

  if (lang === 'curl') {
    return curlRequest(`${ctx.baseUrl}${ctx.path}`, bearerHeaders(ctx), body)
  }
  return [
    'import os',
    'from openai import OpenAI',
    '',
    'client = OpenAI(',
    `    base_url="${ctx.baseUrl}/v1",`,
    `    api_key=os.environ["${ctx.apiKeyEnv}"],`,
    ')',
    '',
    'response = client.responses.create(',
    `    model="${ctx.model}",`,
    `    input="${CHAT_PROMPT}",`,
    ')',
    '',
    'print(response.output_text)',
  ].join('\n')
}

function responsesCompactSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  const body = {
    model: ctx.model,
    input: [{ role: 'user', content: CHAT_PROMPT }],
  }

  if (lang === 'curl') {
    return [
      '# Sends the conversation so far and stores the compacted form the next',
      '# request reuses. Keep the returned encrypted_content verbatim.',
      curlRequest(`${ctx.baseUrl}${ctx.path}`, bearerHeaders(ctx), body),
    ].join('\n')
  }
  return [
    'import os',
    'import requests',
    '',
    `response = requests.post(`,
    `    "${ctx.baseUrl}${ctx.path}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    '    json={',
    `        "model": "${ctx.model}",`,
    '        "input": [{"role": "user", "content": ' + `"${CHAT_PROMPT}"}],`,
    '    },',
    '    timeout=120,',
    ')',
    'print(response.json()["output"])',
  ].join('\n')
}

// ---------------------------------------------------------------------------
// Anthropic Messages
// ---------------------------------------------------------------------------

function anthropicSamples(lang: ApiSampleLanguage, ctx: SampleContext): string {
  const body = {
    model: ctx.model,
    max_tokens: 1024,
    messages: [{ role: 'user', content: CHAT_PROMPT }],
  }

  if (lang === 'curl') {
    return curlRequest(
      `${ctx.baseUrl}${ctx.path}`,
      [
        `x-api-key: $${ctx.apiKeyEnv}`,
        'anthropic-version: 2023-06-01',
        'Content-Type: application/json',
      ],
      body
    )
  }
  return [
    'import os',
    'import anthropic',
    '',
    'client = anthropic.Anthropic(',
    `    base_url="${ctx.baseUrl}",`,
    `    api_key=os.environ["${ctx.apiKeyEnv}"],`,
    ')',
    '',
    'message = client.messages.create(',
    `    model="${ctx.model}",`,
    '    max_tokens=1024,',
    `    messages=[{"role": "user", "content": "${CHAT_PROMPT}"}],`,
    ')',
    '',
    'print(message.content[0].text)',
  ].join('\n')
}

// ---------------------------------------------------------------------------
// Gemini generateContent
// ---------------------------------------------------------------------------

function geminiSamples(lang: ApiSampleLanguage, ctx: SampleContext): string {
  const body = { contents: [{ parts: [{ text: CHAT_PROMPT }] }] }

  if (lang === 'curl') {
    return [
      `curl "${ctx.baseUrl}${ctx.path}?key=$${ctx.apiKeyEnv}" \\`,
      '  -H "Content-Type: application/json" \\',
      `  -d '${indentedJson(body, '     ')}'`,
    ].join('\n')
  }
  return [
    'import os',
    'import requests',
    '',
    'response = requests.post(',
    `    "${ctx.baseUrl}${ctx.path}",`,
    `    params={"key": os.environ["${ctx.apiKeyEnv}"]},`,
    '    json={"contents": [{"parts": [{"text": ' + `"${CHAT_PROMPT}"}]}]},`,
    '    timeout=120,',
    ')',
    'print(response.json()["candidates"][0]["content"]["parts"][0]["text"])',
  ].join('\n')
}

// ---------------------------------------------------------------------------
// System One
// ---------------------------------------------------------------------------

function systemOneModelsSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  if (lang === 'curl') {
    return curlRequest(`${ctx.baseUrl}${ctx.path}`, [
      `Authorization: Bearer $${ctx.apiKeyEnv}`,
    ])
  }
  return [
    'import os',
    'import requests',
    '',
    'response = requests.get(',
    `    "${ctx.baseUrl}${ctx.path}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    '    timeout=30,',
    ')',
    'print([model["name"] for model in response.json()["models"]])',
  ].join('\n')
}

function systemOneDecideSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  const body = { ...SYSTEM_ONE_BODY, model: ctx.model }

  if (lang === 'curl') {
    return curlRequest(`${ctx.baseUrl}${ctx.path}`, bearerHeaders(ctx), body)
  }
  return [
    'import os',
    'import requests',
    '',
    'body = {',
    `    "model": "${ctx.model}",`,
    `    "state": "${SYSTEM_ONE_BODY.state}",`,
    '    "questions": {',
    '        "verdict": {',
    '            "type": "choice",',
    '            "instructions": "Is the queue lossy?",',
    '            "criteria": {"lossy": "Messages can be lost.", "reliable": "No message is lost."},',
    '        },',
    '        "confidence": {',
    '            "type": "score",',
    '            "instructions": "How sure are you about the verdict?",',
    '            "criteria": ["unsure", "fairly sure", "certain"],',
    '        },',
    '    },',
    '}',
    '',
    'response = requests.post(',
    `    "${ctx.baseUrl}${ctx.path}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    '    json=body,',
    '    timeout=120,',
    ')',
    'print(response.json()["answers"]["verdict"])',
  ].join('\n')
}

// ---------------------------------------------------------------------------
// Moonshot Service
// ---------------------------------------------------------------------------

function moonshotSearchSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  // num_results is shown because the endpoint returns nine results by default
  // and a caller that wants fewer has to ask for it.
  const body = { text_query: 'BIRD routing daemon configuration', num_results: 5 }
  const url = `${ctx.baseUrl}${ctx.path}`

  if (lang === 'curl') {
    return curlRequest(url, bearerHeaders(ctx), body)
  }
  return [
    'import os',
    'import requests',
    '',
    'response = requests.post(',
    `    "${url}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    `    json={"text_query": "${body.text_query}", "num_results": ${body.num_results}},`,
    '    timeout=60,',
    ')',
    'for result in response.json()["search_results"]:',
    '    print(result["title"], result["url"])',
  ].join('\n')
}

function moonshotFetchSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  const body = { url: 'https://example.com/article' }
  const url = `${ctx.baseUrl}${ctx.path}`

  if (lang === 'curl') {
    return [
      '# The answer is text/markdown, not JSON: pipe it straight into a file.',
      curlRequest(url, bearerHeaders(ctx), body),
    ].join('\n')
  }
  return [
    'import os',
    'import requests',
    '',
    'response = requests.post(',
    `    "${url}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    `    json={"url": "${body.url}"},`,
    '    timeout=60,',
    ')',
    '# Content-Type is text/markdown; response.text is the extracted body.',
    'print(response.text[:500])',
  ].join('\n')
}

// ---------------------------------------------------------------------------
// DeepSeek tools
// ---------------------------------------------------------------------------

function deepSeekSearchSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  const body = { query: 'BIRD routing daemon configuration', count: 10 }
  const url = `${ctx.baseUrl}${ctx.path}`

  if (lang === 'curl') {
    return curlRequest(url, bearerHeaders(ctx), body)
  }
  return [
    'import os',
    'import requests',
    '',
    'response = requests.post(',
    `    "${url}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    `    json={"query": "${body.query}", "count": 10},`,
    '    timeout=60,',
    ')',
    'for result in response.json()["results"]:',
    '    print(result["title"], result["url"])',
  ].join('\n')
}

function deepSeekFetchSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  const body = { url: 'https://example.com/article' }
  const url = `${ctx.baseUrl}${ctx.path}`

  if (lang === 'curl') {
    return curlRequest(url, bearerHeaders(ctx), body)
  }
  return [
    'import os',
    'import requests',
    '',
    'response = requests.post(',
    `    "${url}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    `    json={"url": "${body.url}"},`,
    '    timeout=60,',
    ')',
    'payload = response.json()',
    'print(payload["title"])',
    'print(payload["content"][:500])',
  ].join('\n')
}

function alphaSearchSamples(
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string {
  const body = { query: 'BIRD routing daemon configuration' }
  const url = `${ctx.baseUrl}${ctx.path}`

  if (lang === 'curl') {
    return curlRequest(url, bearerHeaders(ctx), body)
  }
  return [
    'import os',
    'import requests',
    '',
    'response = requests.post(',
    `    "${url}",`,
    `    headers={"Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}"},`,
    `    json={"query": "${body.query}"},`,
    '    timeout=60,',
    ')',
    'print(response.json())',
  ].join('\n')
}

const SAMPLE_BUILDERS: Record<
  string,
  (lang: ApiSampleLanguage, ctx: SampleContext) => string
> = {
  'chat-completions': chatSamples,
  models: chatModelsSamples,
  responses: responsesSamples,
  'responses-compact': responsesCompactSamples,
  messages: anthropicSamples,
  'generate-content': geminiSamples,
  'systemone-models': systemOneModelsSamples,
  'systemone-decide': systemOneDecideSamples,
  'moonshot-search': moonshotSearchSamples,
  'moonshot-fetch': moonshotFetchSamples,
  'tools-search': deepSeekSearchSamples,
  'tools-fetch': deepSeekFetchSamples,
  'alpha-search': alphaSearchSamples,
}

/** The runnable example for one endpoint, or null when none is written yet. */
export function buildEndpointSample(
  endpointId: string,
  lang: ApiSampleLanguage,
  ctx: SampleContext
): string | null {
  const builder = SAMPLE_BUILDERS[endpointId]
  return builder ? builder(lang, ctx) : null
}
