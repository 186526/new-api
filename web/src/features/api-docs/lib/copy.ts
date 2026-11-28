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
import type { TFunction } from 'i18next'

import type { ApiProtocolId } from '../constants'

/**
 * Every user-visible string on the page, written as a literal `t()` call.
 *
 * Each call passes the English text as `defaultValue`. The locale JSON files
 * are maintained outside this feature: until they carry an `apiDocs.*` key,
 * `t()` answers with the key itself, so the fallback is what a reader sees.
 * A translation replaces it as soon as one exists, without touching this file.
 */
export function protocolTitle(t: TFunction, id: ApiProtocolId): string {
  switch (id) {
    case 'openai-chat':
      return t('apiDocs.protocol.openaiChat.title', {
        defaultValue: 'OpenAI Chat Completions',
      })
    case 'openai-responses':
      return t('apiDocs.protocol.openaiResponses.title', {
        defaultValue: 'OpenAI Responses',
      })
    case 'anthropic':
      return t('apiDocs.protocol.anthropic.title', {
        defaultValue: 'Anthropic Messages',
      })
    case 'gemini':
      return t('apiDocs.protocol.gemini.title', {
        defaultValue: 'Gemini generateContent',
      })
    case 'systemone':
      return t('apiDocs.protocol.systemone.title', {
        defaultValue: 'System One',
      })
    case 'moonshot-service':
      return t('apiDocs.protocol.moonshotService.title', {
        defaultValue: 'Moonshot Service',
      })
    case 'deepseek-tools':
      return t('apiDocs.protocol.deepseekTools.title', {
        defaultValue: 'DeepSeek Search and Fetch',
      })
  }
}

export function protocolSummary(t: TFunction, id: ApiProtocolId): string {
  switch (id) {
    case 'openai-chat':
      return t('apiDocs.protocol.openaiChat.summary', {
        defaultValue:
          'Drop-in OpenAI chat endpoint. Point any OpenAI SDK at /v1 and send a message list.',
      })
    case 'openai-responses':
      return t('apiDocs.protocol.openaiResponses.summary', {
        defaultValue:
          'The Responses API: send input, read output_text. /compact stores the compacted conversation.',
      })
    case 'anthropic':
      return t('apiDocs.protocol.anthropic.summary', {
        defaultValue:
          'Claude Messages contract with the anthropic-version header, served on /v1/messages.',
      })
    case 'gemini':
      return t('apiDocs.protocol.gemini.summary', {
        defaultValue:
          'Native Gemini shape: contents, parts and candidates, with the model name in the path.',
      })
    case 'systemone':
      return t('apiDocs.protocol.systemone.summary', {
        defaultValue:
          'TypeSafe decision models answer typed questions about text and state with probabilities, labels or scores.',
      })
    case 'moonshot-service':
      return t('apiDocs.protocol.moonshotService.summary', {
        defaultValue:
          'Search and fetch for Kimi Code. Set base_url to /moonshot.service/v1; the client appends /search or /fetch itself.',
      })
    case 'deepseek-tools':
      return t('apiDocs.protocol.deepseekTools.summary', {
        defaultValue:
          'The upstream tools contract, plus the Codex standalone search endpoint.',
      })
  }
}

/** Empty string when the protocol has no note to show. */
export function protocolNote(t: TFunction, id: ApiProtocolId): string {
  switch (id) {
    case 'systemone':
      return t('apiDocs.protocol.systemone.note', {
        defaultValue:
          'Decision models are named jev-*. Only this contract serves them; a chat endpoint refuses them.',
      })
    case 'moonshot-service':
      return t('apiDocs.protocol.moonshotService.note', {
        defaultValue:
          'The client treats any non-200 answer as a failed call. /search answers JSON; /fetch answers text/markdown, not JSON.',
      })
    case 'deepseek-tools':
      return t('apiDocs.protocol.deepseekTools.modelNote', {
        defaultValue:
          '/v1/tools/* selects tools-search or tools-fetch automatically. /v1/alpha/search requires a model your key can call for Alpha Search.',
      })
    default:
      return ''
  }
}

export function protocolAuth(t: TFunction, id: ApiProtocolId): string {
  switch (id) {
    case 'anthropic':
      return t('apiDocs.auth.anthropic', {
        defaultValue: 'x-api-key: <API key> plus anthropic-version: 2023-06-01',
      })
    case 'gemini':
      return t('apiDocs.auth.geminiKey', {
        defaultValue:
          'The API key goes in the key query parameter or the x-goog-api-key header.',
      })
    default:
      return t('apiDocs.auth.bearer', {
        defaultValue: 'Authorization: Bearer <API key>',
      })
  }
}

export function endpointDescription(t: TFunction, id: string): string {
  switch (id) {
    case 'chat-completions':
      return t('apiDocs.endpoint.chatCompletions', {
        defaultValue: 'Create a chat completion from a message list.',
      })
    case 'models':
      return t('apiDocs.endpoint.models', {
        defaultValue: 'List the models this key can call.',
      })
    case 'responses':
      return t('apiDocs.endpoint.responses', {
        defaultValue: 'Create a response from a prompt or input items.',
      })
    case 'responses-compact':
      return t('apiDocs.endpoint.responsesCompact', {
        defaultValue: 'Compact a conversation and return the stored form.',
      })
    case 'messages':
      return t('apiDocs.endpoint.messages', {
        defaultValue: 'Create a Claude message.',
      })
    case 'generate-content':
      return t('apiDocs.endpoint.generateContent', {
        defaultValue: 'Generate text with a Gemini model.',
      })
    case 'systemone-models':
      return t('apiDocs.endpoint.systemoneModels', {
        defaultValue: 'List the decision models this key can call.',
      })
    case 'systemone-decide':
      return t('apiDocs.endpoint.systemoneDecide', {
        defaultValue: 'Ask typed questions about a state and read the answers.',
      })
    case 'moonshot-search':
      return t('apiDocs.endpoint.moonshotSearch', {
        defaultValue: 'Search the web and return ranked results.',
      })
    case 'moonshot-fetch':
      return t('apiDocs.endpoint.moonshotFetch', {
        defaultValue:
          'Fetch a page and return its extracted body as text/markdown.',
      })
    case 'tools-search':
      return t('apiDocs.endpoint.toolsSearch', {
        defaultValue: 'Search the web through the DeepSeek contract.',
      })
    case 'tools-fetch':
      return t('apiDocs.endpoint.toolsFetch', {
        defaultValue:
          'Fetch a page through the DeepSeek contract; the answer is JSON.',
      })
    case 'alpha-search':
      return t('apiDocs.endpoint.alphaSearch', {
        defaultValue: 'Standalone search for Codex-style clients.',
      })
    default:
      return ''
  }
}
