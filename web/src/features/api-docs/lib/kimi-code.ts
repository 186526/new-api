export const KIMI_CODE_API_KEY_PLACEHOLDER = 'YOUR_NEW_API_KEY'

function tomlString(value: string): string {
  return JSON.stringify(value).replaceAll('\u007f', '\\u007F')
}

export function buildKimiCodeConfig(
  baseUrl: string,
  apiKey = KIMI_CODE_API_KEY_PLACEHOLDER
): string {
  const serviceUrl = `${baseUrl.replace(/\/+$/, '')}/moonshot.service/v1`
  const key = tomlString(apiKey)
  return [
    '[services.moonshot_search]',
    `base_url = ${tomlString(`${serviceUrl}/search`)}`,
    `api_key = ${key}`,
    '',
    '[services.moonshot_fetch]',
    `base_url = ${tomlString(`${serviceUrl}/fetch`)}`,
    `api_key = ${key}`,
  ].join('\n')
}
