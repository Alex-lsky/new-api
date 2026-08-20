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

import type { Channel } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  buildSettingJSON,
  channelFormSchema,
  transformChannelToFormDefaults,
} from '../channel-form'
import { hasAdvancedSettingsErrors } from '../channel-form-errors'

function channelWithSetting(setting: Record<string, unknown>): Channel {
  return {
    id: 1,
    type: 1,
    key: '',
    status: 1,
    name: 'Hosted tools',
    weight: 0,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    other: '',
    balance: 0,
    balance_updated_time: 0,
    models: 'gpt-5',
    group: 'default',
    used_quota: 0,
    other_info: '',
    remark: '',
    max_input_tokens: 0,
    channel_info: {
      is_multi_key: false,
      multi_key_size: 0,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
    setting: JSON.stringify(setting),
    settings: '{}',
  }
}

describe('hosted channel tools', () => {
  test('loads image recognition provider refs while preserving other refs', () => {
    const values = transformChannelToFormDefaults(
      channelWithSetting({
        emulate_tool_types: [
          'web_search',
          'image_recognition',
          'image_generation',
        ],
        emulated_tool_backends: {
          web_search: { ref: 'search-provider' },
          image_recognition: { ref: 'vision-provider' },
          image_generation: { ref: 'image-provider' },
        },
      })
    )

    expect(values.hosted_web_search).toEqual({
      action: 'emulate',
      provider_ref: 'search-provider',
    })
    expect(values.hosted_image_recognition).toEqual({
      action: 'emulate',
      provider_ref: 'vision-provider',
    })
    expect(values.hosted_image_generation).toEqual({
      action: 'emulate',
      provider_ref: 'image-provider',
    })
  })

  test('serializes image recognition actions without dropping other hosted tools', () => {
    const setting = JSON.parse(
      buildSettingJSON({
        ...CHANNEL_FORM_DEFAULT_VALUES,
        hosted_web_search: {
          action: 'emulate',
          provider_ref: 'search-provider',
        },
        hosted_image_recognition: {
          action: 'emulate',
          provider_ref: 'vision-provider',
        },
        hosted_image_generation: {
          action: 'strip',
          provider_ref: '',
        },
      })
    )

    expect(setting.emulate_tool_types).toEqual([
      'web_search',
      'image_recognition',
    ])
    expect(setting.emulated_tool_backends).toEqual({
      web_search: { ref: 'search-provider' },
      image_recognition: { ref: 'vision-provider' },
    })
    expect(setting.strip_tool_types).toEqual(['image_generation'])
  })

  test('requires an image recognition provider only for emulate mode', () => {
    const invalid = channelFormSchema.safeParse({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Hosted tools',
      models: 'gpt-5',
      hosted_image_recognition: {
        action: 'emulate',
        provider_ref: ' ',
      },
    })

    expect(invalid.success).toBe(false)
    if (!invalid.success) {
      expect(invalid.error.issues).toContainEqual(
        expect.objectContaining({
          path: ['hosted_image_recognition', 'provider_ref'],
          message: 'Select a global tool provider to emulate this tool',
        })
      )
    }

    expect(
      channelFormSchema.safeParse({
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'Hosted tools',
        models: 'gpt-5',
        hosted_image_recognition: {
          action: 'strip',
          provider_ref: '',
        },
      }).success
    ).toBe(true)
  })

  test('classifies hosted image recognition errors as advanced settings errors', () => {
    expect(
      hasAdvancedSettingsErrors({
        hosted_image_recognition: {
          provider_ref: { message: 'Select a provider' },
        },
      })
    ).toBe(true)
  })
})
