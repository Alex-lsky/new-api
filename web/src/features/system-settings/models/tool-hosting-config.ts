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

export const TOOL_KINDS = [
  'web_search',
  'image_recognition',
  'image_generation',
] as const

export type ToolKind = (typeof TOOL_KINDS)[number]
export type CredentialSource = 'channel' | 'manual'

export type ExecutorConfig = {
  type: string
  kind: ToolKind
  label: string
  supportsChannel: boolean
  channelRequiresModel?: boolean
  channelCredentialScope?: 'full' | 'key_only'
  requiresApiKey: boolean
  requiresApiBase: boolean
  supportsApiBase?: boolean
  requiresModel: boolean
  modelLabel: 'Model' | 'Search engine'
  defaultModel: string
  defaultApiBase: string
}

export const EXECUTOR_CONFIGS: Record<ToolKind, ExecutorConfig[]> = {
  web_search: [
    {
      type: 'gemini',
      kind: 'web_search',
      label: 'Gemini Grounding',
      supportsChannel: true,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: true,
      modelLabel: 'Model',
      defaultModel: 'gemini-2.5-flash',
      defaultApiBase: 'https://generativelanguage.googleapis.com/v1beta',
    },
    {
      type: 'zhipu',
      kind: 'web_search',
      label: 'Zhipu Web Search API',
      supportsChannel: false,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: true,
      modelLabel: 'Search engine',
      defaultModel: 'search_std',
      defaultApiBase: 'https://open.bigmodel.cn/api/paas/v4',
    },
    {
      type: 'zhipu_code_plan_search_mcp',
      kind: 'web_search',
      label: 'Code Plan Search MCP',
      supportsChannel: true,
      channelRequiresModel: false,
      channelCredentialScope: 'key_only',
      requiresApiKey: true,
      requiresApiBase: false,
      supportsApiBase: true,
      requiresModel: false,
      modelLabel: 'Model',
      defaultModel: '',
      defaultApiBase: 'https://open.bigmodel.cn/api/mcp/web_search_prime/mcp',
    },
    {
      type: 'tavily',
      kind: 'web_search',
      label: 'Tavily',
      supportsChannel: false,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: false,
      modelLabel: 'Model',
      defaultModel: '',
      defaultApiBase: 'https://api.tavily.com',
    },
    {
      type: 'brave',
      kind: 'web_search',
      label: 'Brave Search',
      supportsChannel: false,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: false,
      modelLabel: 'Model',
      defaultModel: '',
      defaultApiBase: 'https://api.search.brave.com/res/v1',
    },
    {
      type: 'bocha',
      kind: 'web_search',
      label: 'Bocha Search',
      supportsChannel: false,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: false,
      modelLabel: 'Model',
      defaultModel: '',
      defaultApiBase: 'https://api.bochaai.com/v1',
    },
    {
      type: 'searxng',
      kind: 'web_search',
      label: 'SearXNG',
      supportsChannel: false,
      requiresApiKey: false,
      requiresApiBase: true,
      requiresModel: false,
      modelLabel: 'Model',
      defaultModel: '',
      defaultApiBase: '',
    },
    {
      type: 'http_json',
      kind: 'web_search',
      label: 'HTTP JSON',
      supportsChannel: false,
      requiresApiKey: false,
      requiresApiBase: true,
      requiresModel: false,
      modelLabel: 'Model',
      defaultModel: '',
      defaultApiBase: '',
    },
  ],
  image_recognition: [
    {
      type: 'gemini',
      kind: 'image_recognition',
      label: 'Gemini Vision',
      supportsChannel: true,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: true,
      modelLabel: 'Model',
      defaultModel: 'gemini-2.5-flash',
      defaultApiBase: 'https://generativelanguage.googleapis.com/v1beta',
    },
    {
      type: 'openai',
      kind: 'image_recognition',
      label: 'OpenAI-compatible Vision',
      supportsChannel: true,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: true,
      modelLabel: 'Model',
      defaultModel: 'gpt-4o-mini',
      defaultApiBase: 'https://api.openai.com/v1',
    },
    {
      type: 'zhipu_code_plan_vision_mcp',
      kind: 'image_recognition',
      label: 'Code Plan Vision MCP',
      supportsChannel: true,
      channelRequiresModel: false,
      channelCredentialScope: 'key_only',
      requiresApiKey: true,
      requiresApiBase: false,
      supportsApiBase: false,
      requiresModel: false,
      modelLabel: 'Model',
      defaultModel: '',
      defaultApiBase: '',
    },
  ],
  image_generation: [
    {
      type: 'openai_images',
      kind: 'image_generation',
      label: 'OpenAI-compatible Images',
      supportsChannel: true,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: true,
      modelLabel: 'Model',
      defaultModel: 'gpt-image-1',
      defaultApiBase: 'https://api.openai.com/v1',
    },
    {
      type: 'gemini_images',
      kind: 'image_generation',
      label: 'Gemini Image Generation',
      supportsChannel: true,
      requiresApiKey: true,
      requiresApiBase: false,
      requiresModel: true,
      modelLabel: 'Model',
      defaultModel: 'gemini-2.5-flash-image',
      defaultApiBase: 'https://generativelanguage.googleapis.com/v1beta',
    },
  ],
}

export const EXECUTORS: Record<ToolKind, string[]> = {
  web_search: EXECUTOR_CONFIGS.web_search.map((executor) => executor.type),
  image_recognition: EXECUTOR_CONFIGS.image_recognition.map(
    (executor) => executor.type
  ),
  image_generation: EXECUTOR_CONFIGS.image_generation.map(
    (executor) => executor.type
  ),
}

export type ProviderRow = {
  uid: string
  name: string
  kind: string
  type: string
  credentials: CredentialSource
  channel_id: string
  model: string
  api_key: string
  api_base: string
  request_body: string
  result_path: string
}

export type RawProviderOption = {
  kind?: string
  type?: string
  channel_id?: number
  model?: string
  api_key?: string
  api_base?: string
  extra?: Record<string, string>
}

export type ProviderField =
  | 'name'
  | 'kind'
  | 'type'
  | 'channel_id'
  | 'model'
  | 'api_key'
  | 'api_base'
  | 'request_body'
  | 'result_path'

export type ProviderErrors = Partial<Record<ProviderField, string>>
export type ProviderErrorMap = Record<string, ProviderErrors>

let uidCounter = 0

export function nextProviderUid(): string {
  uidCounter += 1
  return `uid-${Date.now().toString(36)}-${uidCounter}`
}

export function isToolKind(value: string): value is ToolKind {
  return TOOL_KINDS.includes(value as ToolKind)
}

export function getExecutorConfig(
  kind: string,
  type: string
): ExecutorConfig | undefined {
  if (!isToolKind(kind)) return undefined
  return EXECUTOR_CONFIGS[kind].find((executor) => executor.type === type)
}

export function getExecutorDefaultApiBase(
  executor: ExecutorConfig,
  credentials: CredentialSource
): string {
  if (
    credentials === 'manual' ||
    executor.channelCredentialScope === 'key_only'
  ) {
    return executor.defaultApiBase
  }
  return ''
}

export function createProviderRow(kind: ToolKind = 'web_search'): ProviderRow {
  const executor = EXECUTOR_CONFIGS[kind][0]
  const credentials = executor.supportsChannel ? 'channel' : 'manual'
  return {
    uid: nextProviderUid(),
    name: '',
    kind,
    type: executor.type,
    credentials,
    channel_id: '',
    model: credentials === 'channel' ? '' : executor.defaultModel,
    api_key: '',
    api_base: getExecutorDefaultApiBase(executor, credentials),
    request_body: '',
    result_path: '',
  }
}

function parseOptionObject<T>(value: string): T {
  try {
    const parsed = JSON.parse(value || '{}')
    return (parsed && typeof parsed === 'object' ? parsed : {}) as T
  } catch {
    return {} as T
  }
}

export function providersFromOptions(value: string): ProviderRow[] {
  const raw = parseOptionObject<Record<string, RawProviderOption>>(value)
  return Object.entries(raw).map(([name, provider]) => {
    const kind = provider.kind || 'web_search'
    const type = provider.type || 'tavily'
    const executor = getExecutorConfig(kind, type)
    const credentials =
      provider.channel_id && provider.channel_id > 0 ? 'channel' : 'manual'
    const apiBase = executor
      ? provider.api_base || getExecutorDefaultApiBase(executor, credentials)
      : provider.api_base || ''

    return {
      uid: nextProviderUid(),
      name,
      kind,
      type,
      credentials,
      channel_id: provider.channel_id ? String(provider.channel_id) : '',
      model:
        executor &&
        ((credentials === 'channel' &&
          executor.channelRequiresModel === false) ||
          (credentials === 'manual' && !executor.requiresModel))
          ? ''
          : provider.model ||
            (credentials === 'manual' ? executor?.defaultModel || '' : ''),
      api_key: provider.api_key || '',
      api_base: executor?.supportsApiBase === false ? '' : apiBase,
      request_body: provider.extra?.request_body || '',
      result_path: provider.extra?.result_path || '',
    }
  })
}

export function providerToOption(
  provider: ProviderRow
): Record<string, unknown> {
  const base: Record<string, unknown> = {
    kind: provider.kind,
    type: provider.type,
  }
  const executor = getExecutorConfig(provider.kind, provider.type)
  if (provider.credentials === 'channel' && provider.channel_id) {
    base.channel_id = Number(provider.channel_id) || 0
    if (executor?.channelRequiresModel !== false && provider.model.trim()) {
      base.model = provider.model.trim()
    }
    if (executor?.supportsApiBase !== false && provider.api_base.trim()) {
      base.api_base = provider.api_base.trim()
    }
    return base
  }
  if (provider.api_key.trim()) base.api_key = provider.api_key.trim()
  if (executor?.requiresModel !== false && provider.model.trim()) {
    base.model = provider.model.trim()
  }
  if (executor?.supportsApiBase !== false && provider.api_base.trim()) {
    base.api_base = provider.api_base.trim()
  }
  if (provider.type === 'http_json') {
    const extra: Record<string, string> = {}
    if (provider.request_body.trim()) {
      extra.request_body = provider.request_body.trim()
    }
    if (provider.result_path.trim()) {
      extra.result_path = provider.result_path.trim()
    }
    base.extra = extra
  }
  return base
}

export function providersToOption(
  rows: ProviderRow[]
): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  rows.forEach((provider) => {
    out[provider.name.trim()] = providerToOption(provider)
  })
  return out
}

function isHttpURL(value: string): boolean {
  try {
    const url = new URL(value.replaceAll('{query}', 'query'))
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

export function validateProviders(rows: ProviderRow[]): ProviderErrorMap {
  const errors: ProviderErrorMap = {}
  const nameCounts = new Map<string, number>()
  rows.forEach((provider) => {
    const name = provider.name.trim()
    if (name) nameCounts.set(name, (nameCounts.get(name) ?? 0) + 1)
  })

  rows.forEach((provider) => {
    const rowErrors: ProviderErrors = {}
    const name = provider.name.trim()
    const executor = getExecutorConfig(provider.kind, provider.type)

    if (!name) {
      rowErrors.name = 'Provider name is required'
    } else if ((nameCounts.get(name) ?? 0) > 1) {
      rowErrors.name = 'Provider names must be unique'
    }
    if (!isToolKind(provider.kind)) {
      rowErrors.kind = 'Select a valid tool type'
    }
    if (!executor) {
      rowErrors.type = 'Select a valid executor'
    } else if (executor.supportsChannel && provider.credentials === 'channel') {
      if (!provider.channel_id) rowErrors.channel_id = 'Select a channel'
      if (executor.channelRequiresModel !== false && !provider.model.trim()) {
        rowErrors.model = 'Select a model'
      }
      if (provider.api_base.trim() && !isHttpURL(provider.api_base.trim())) {
        rowErrors.api_base = 'Enter a valid HTTP(S) URL'
      }
    } else {
      if (executor.requiresApiKey && !provider.api_key.trim()) {
        rowErrors.api_key = 'API key is required'
      }
      if (executor.requiresModel && !provider.model.trim()) {
        rowErrors.model =
          executor.modelLabel === 'Search engine'
            ? 'Search engine is required'
            : 'Model is required'
      }
      if (executor.requiresApiBase && !provider.api_base.trim()) {
        rowErrors.api_base = 'API base is required'
      }
      if (provider.api_base.trim() && !isHttpURL(provider.api_base.trim())) {
        rowErrors.api_base = 'Enter a valid HTTP(S) URL'
      }
      if (
        provider.type === 'http_json' &&
        provider.api_base.trim() &&
        !provider.api_base.includes('{query}')
      ) {
        rowErrors.api_base = 'Request URL must contain {query}'
      }
      if (provider.type === 'http_json') {
        if (!provider.result_path.trim()) {
          rowErrors.result_path = 'Result path is required'
        }
        if (provider.request_body.trim()) {
          if (!provider.request_body.includes('{query}')) {
            rowErrors.request_body = 'Request body must contain {query}'
          } else {
            try {
              JSON.parse(provider.request_body)
            } catch {
              rowErrors.request_body = 'Request body must be valid JSON'
            }
          }
        }
      }
    }

    if (Object.keys(rowErrors).length > 0) errors[provider.uid] = rowErrors
  })
  return errors
}

export function channelModels(models: string | undefined): string[] {
  if (!models) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const model of models.split(',')) {
    const trimmed = model.trim()
    if (trimmed && !seen.has(trimmed)) {
      seen.add(trimmed)
      out.push(trimmed)
    }
  }
  return out
}
