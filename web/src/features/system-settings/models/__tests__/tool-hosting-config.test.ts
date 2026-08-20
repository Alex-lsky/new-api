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
import { describe, expect, test } from 'vitest'

import {
  createProviderRow,
  getExecutorConfig,
  providersFromOptions,
  providersToOption,
  validateProviders,
} from '../tool-hosting-config'

describe('tool hosting provider configuration', () => {
  test('loads official defaults for direct built-in providers', () => {
    const providers = providersFromOptions(
      JSON.stringify({
        search: { kind: 'web_search', type: 'tavily', api_key: 'key' },
        zhipu: { kind: 'web_search', type: 'zhipu', api_key: 'key' },
      })
    )

    expect(providers[0]).toMatchObject({
      api_base: 'https://api.tavily.com',
      credentials: 'manual',
    })
    expect(providers[1]).toMatchObject({
      api_base: 'https://open.bigmodel.cn/api/paas/v4',
      credentials: 'manual',
      model: 'search_std',
    })
  })

  test('keeps channel-backed providers free of manual credentials', () => {
    const providers = providersFromOptions(
      JSON.stringify({
        vision: {
          kind: 'image_recognition',
          type: 'gemini',
          channel_id: 6,
          model: 'gemini-2.5-flash',
        },
      })
    )

    expect(providersToOption(providers)).toEqual({
      vision: {
        kind: 'image_recognition',
        type: 'gemini',
        channel_id: 6,
        model: 'gemini-2.5-flash',
      },
    })
  })

  test('preserves generic HTTP request mapping', () => {
    const providers = providersFromOptions(
      JSON.stringify({
        custom: {
          kind: 'web_search',
          type: 'http_json',
          api_base: 'https://search.example?q={query}',
          extra: {
            request_body: '{"q":"{query}"}',
            result_path: 'data.results',
          },
        },
      })
    )

    expect(validateProviders(providers)).toEqual({})
    expect(providersToOption(providers)).toEqual({
      custom: {
        kind: 'web_search',
        type: 'http_json',
        api_base: 'https://search.example?q={query}',
        extra: {
          request_body: '{"q":"{query}"}',
          result_path: 'data.results',
        },
      },
    })
  })

  test('rejects duplicate names and missing direct API credentials', () => {
    const first = createProviderRow()
    first.name = 'duplicate'
    first.credentials = 'manual'
    first.model = 'gemini-2.5-flash'
    const second = createProviderRow()
    second.name = ' duplicate '

    const errors = validateProviders([first, second])

    expect(errors[first.uid]).toMatchObject({
      name: 'Provider names must be unique',
      api_key: 'API key is required',
    })
    expect(errors[second.uid]).toMatchObject({
      name: 'Provider names must be unique',
      channel_id: 'Select a channel',
      model: 'Select a model',
    })
  })

  test('configures Code Plan MCP executors without model fields', () => {
    expect(
      getExecutorConfig('web_search', 'zhipu_code_plan_search_mcp')
    ).toMatchObject({
      label: 'Code Plan Search MCP',
      supportsChannel: true,
      channelRequiresModel: false,
      channelCredentialScope: 'key_only',
      requiresApiKey: true,
      requiresModel: false,
      supportsApiBase: true,
      defaultApiBase: 'https://open.bigmodel.cn/api/mcp/web_search_prime/mcp',
    })
    expect(
      getExecutorConfig('image_recognition', 'zhipu_code_plan_vision_mcp')
    ).toMatchObject({
      label: 'Code Plan Vision MCP',
      supportsChannel: true,
      channelRequiresModel: false,
      channelCredentialScope: 'key_only',
      requiresApiKey: true,
      requiresModel: false,
      supportsApiBase: false,
      defaultApiBase: '',
    })
  })

  test('serializes Code Plan Search MCP channel mode with its remote endpoint', () => {
    const provider = providersFromOptions(
      JSON.stringify({
        search: {
          kind: 'web_search',
          type: 'zhipu_code_plan_search_mcp',
          channel_id: 8,
        },
      })
    )[0]

    expect(provider).toMatchObject({
      credentials: 'channel',
      model: '',
      api_base: 'https://open.bigmodel.cn/api/mcp/web_search_prime/mcp',
    })
    expect(validateProviders([provider])).toEqual({})
    expect(providersToOption([provider])).toEqual({
      search: {
        kind: 'web_search',
        type: 'zhipu_code_plan_search_mcp',
        channel_id: 8,
        api_base: 'https://open.bigmodel.cn/api/mcp/web_search_prime/mcp',
      },
    })
  })

  test('drops stale model and endpoint values for Code Plan Vision MCP', () => {
    const provider = providersFromOptions(
      JSON.stringify({
        vision: {
          kind: 'image_recognition',
          type: 'zhipu_code_plan_vision_mcp',
          api_key: 'key',
          model: 'legacy-model',
          api_base: 'https://legacy.example',
        },
      })
    )[0]

    expect(provider).toMatchObject({
      model: '',
      api_base: '',
    })
    expect(providersToOption([provider])).toEqual({
      vision: {
        kind: 'image_recognition',
        type: 'zhipu_code_plan_vision_mcp',
        api_key: 'key',
      },
    })
  })

  test('keeps executor support scoped to its tool kind', () => {
    expect(getExecutorConfig('web_search', 'openai_images')).toBeUndefined()
    expect(getExecutorConfig('image_generation', 'openai')).toBeUndefined()
    expect(
      getExecutorConfig('web_search', 'zhipu_code_plan_vision_mcp')
    ).toBeUndefined()
    expect(
      getExecutorConfig('image_recognition', 'zhipu_code_plan_search_mcp')
    ).toBeUndefined()
    expect(getExecutorConfig('image_recognition', 'openai')).toMatchObject({
      defaultApiBase: 'https://api.openai.com/v1',
      defaultModel: 'gpt-4o-mini',
    })
  })
})
