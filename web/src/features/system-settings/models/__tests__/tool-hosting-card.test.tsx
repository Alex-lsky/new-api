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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, test, vi } from 'vitest'

import { createProviderRow, type ProviderRow } from '../tool-hosting-config'
import { ToolHostingProviderCard } from '../tool-hosting-provider-card'

function ProviderCardHarness({ initial }: { initial: ProviderRow }) {
  const [provider, setProvider] = useState(initial)
  return (
    <ToolHostingProviderCard
      provider={provider}
      errors={{}}
      channelItems={[{ value: '6', label: 'Gemini channel (#6)' }]}
      models={['gemini-2.5-flash']}
      onChange={(patch) =>
        setProvider((previous) => ({ ...previous, ...patch }))
      }
      onRemove={vi.fn()}
    />
  )
}

describe('tool hosting provider card', () => {
  test('shows only channel and model fields for existing-channel mode', () => {
    const provider = createProviderRow()
    provider.name = 'google-search'

    render(<ProviderCardHarness initial={provider} />)

    expect(screen.getByLabelText('Credential source')).toHaveTextContent(
      'Use existing channel'
    )
    expect(screen.getByLabelText('Channel')).toBeInTheDocument()
    expect(screen.getByLabelText('Model')).toBeInTheDocument()
    expect(screen.queryByLabelText('API key')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('API base')).not.toBeInTheDocument()
  })

  test('switching to direct API reveals manual fields and official defaults', async () => {
    const user = userEvent.setup()
    const provider = createProviderRow()
    provider.name = 'google-search'

    render(<ProviderCardHarness initial={provider} />)

    await user.click(screen.getByLabelText('Credential source'))
    await user.click(screen.getByRole('option', { name: 'Direct API' }))

    expect(screen.getByLabelText('API key')).toBeInTheDocument()
    expect(screen.getByLabelText('Model')).toHaveValue('gemini-2.5-flash')
    expect(screen.queryByLabelText('Channel')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Advanced Settings/ }))

    expect(screen.getByLabelText('API base')).toHaveValue(
      'https://generativelanguage.googleapis.com/v1beta'
    )
  })

  test('uses a channel key without a model for Code Plan Search MCP', async () => {
    const user = userEvent.setup()
    const provider = createProviderRow()
    provider.name = 'code-plan-search'
    provider.type = 'zhipu_code_plan_search_mcp'
    provider.credentials = 'channel'
    provider.api_base = 'https://open.bigmodel.cn/api/mcp/web_search_prime/mcp'

    render(<ProviderCardHarness initial={provider} />)

    expect(screen.getByLabelText('Channel')).toBeInTheDocument()
    expect(screen.queryByLabelText('Model')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('API key')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Advanced Settings/ }))

    expect(screen.getByLabelText('API base')).toHaveValue(
      'https://open.bigmodel.cn/api/mcp/web_search_prime/mcp'
    )
  })

  test('shows only an API key for direct Code Plan Vision MCP', () => {
    const provider = createProviderRow('image_recognition')
    provider.name = 'code-plan-vision'
    provider.type = 'zhipu_code_plan_vision_mcp'
    provider.credentials = 'manual'

    render(<ProviderCardHarness initial={provider} />)

    expect(screen.getByLabelText('API key')).toBeInTheDocument()
    expect(screen.queryByLabelText('Model')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('API base')).not.toBeInTheDocument()
  })

  test('renders generic HTTP fields once without unrelated credentials', () => {
    const provider = createProviderRow()
    provider.name = 'custom-search'
    provider.type = 'http_json'
    provider.credentials = 'manual'
    provider.api_base = 'https://search.example?q={query}'
    provider.result_path = 'data.results'

    render(<ProviderCardHarness initial={provider} />)

    expect(screen.getByLabelText('Request URL')).toHaveValue(
      'https://search.example?q={query}'
    )
    expect(screen.getAllByLabelText('Request URL')).toHaveLength(1)
    expect(screen.getByLabelText('Result path')).toBeInTheDocument()
    expect(screen.getByLabelText('Request body template')).toBeInTheDocument()
    expect(screen.queryByLabelText('API key')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Credential source')).not.toBeInTheDocument()
  })
})
