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
import { type Control, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import type { ChannelFormValues } from '../lib/channel-form'

type HostedToolFieldName = 'hosted_web_search' | 'hosted_image_generation'

interface HostedToolProviderOption {
  value: string
  label: string
  needsApiKey: boolean
  modelPlaceholder?: string
  apiBasePlaceholder?: string
}

const SEARCH_PROVIDERS: HostedToolProviderOption[] = [
  {
    value: 'gemini',
    label: 'Gemini Grounding (Google)',
    needsApiKey: true,
    modelPlaceholder: 'gemini-2.5-flash',
    apiBasePlaceholder: 'https://generativelanguage.googleapis.com/v1beta',
  },
  {
    value: 'zhipu',
    label: 'Zhipu GLM Web Search',
    needsApiKey: true,
    modelPlaceholder: 'search_std',
    apiBasePlaceholder: 'https://open.bigmodel.cn/api/paas/v4',
  },
  {
    value: 'tavily',
    label: 'Tavily',
    needsApiKey: true,
    apiBasePlaceholder: 'https://api.tavily.com',
  },
  {
    value: 'brave',
    label: 'Brave Search',
    needsApiKey: true,
    apiBasePlaceholder: 'https://api.search.brave.com/res/v1',
  },
  {
    value: 'bocha',
    label: 'Bocha',
    needsApiKey: true,
    apiBasePlaceholder: 'https://api.bochaai.com/v1',
  },
  {
    value: 'searxng',
    label: 'SearXNG (self-hosted)',
    needsApiKey: false,
    apiBasePlaceholder: 'https://searxng.example',
  },
  {
    value: 'http_json',
    label: 'Custom JSON endpoint',
    needsApiKey: false,
    apiBasePlaceholder: 'https://example.com/search?q={query}',
  },
]

const IMAGE_PROVIDERS: HostedToolProviderOption[] = [
  {
    value: 'openai_images',
    label: 'OpenAI-compatible Images API',
    needsApiKey: true,
    modelPlaceholder: 'gpt-image-1',
    apiBasePlaceholder: 'https://api.openai.com/v1',
  },
  {
    value: 'gemini_images',
    label: 'Gemini Image (generateContent)',
    needsApiKey: true,
    modelPlaceholder: 'gemini-2.5-flash-image',
    apiBasePlaceholder: 'https://generativelanguage.googleapis.com/v1beta',
  },
]

/**
 * Per-hosted-tool configuration card: pick how this channel treats the tool
 * (leave alone / strip / gateway-executed emulation) and, when emulating,
 * which provider backend executes it.
 */
export function HostedToolField({
  control,
  name,
  toolLabel,
  providers,
}: {
  control: Control<ChannelFormValues>
  name: HostedToolFieldName
  toolLabel: string
  providers: HostedToolProviderOption[]
}) {
  const { t } = useTranslation()
  const action = useWatch({ control, name: `${name}.action` })
  const provider = useWatch({ control, name: `${name}.provider` })
  const providerOption = providers.find((option) => option.value === provider)
  const emulating = action === 'emulate'

  const actionItems = [
    { value: 'none', label: t('Leave to upstream') },
    { value: 'strip', label: t('Strip from requests') },
    { value: 'emulate', label: t('Gateway executes (emulate)') },
    { value: 'exclude', label: t('Exclude from global') },
  ]

  return (
    <div className='space-y-3 rounded-md border p-3'>
      <FormField
        control={control}
        name={`${name}.action`}
        render={({ field }) => (
          <FormItem>
            <FormLabel>{toolLabel}</FormLabel>
            <Select
              items={actionItems}
              value={field.value || 'none'}
              onValueChange={field.onChange}
            >
              <FormControl>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
              </FormControl>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {actionItems.map((item) => (
                    <SelectItem key={item.value} value={item.value}>
                      {item.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <FormDescription>
              {name === 'hosted_web_search'
                ? t(
                    'How this channel treats the hosted web_search tool. "Gateway executes" runs a search backend the gateway owns and restores native web_search_call items; "Exclude from global" opts this tool out of global tool hosting for this channel. Tool types the channel does not pin follow the global Tool Hosting module (bound per model).'
                  )
                : t(
                    'How this channel treats the hosted image_generation tool. "Gateway executes" runs an image backend the gateway owns and restores native image_generation_call items; "Exclude from global" opts this tool out of global tool hosting for this channel. Tool types the channel does not pin follow the global Tool Hosting module (bound per model).'
                  )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      {emulating && (
        <>
          <FormField
            control={control}
            name={`${name}.provider`}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Backend provider')}</FormLabel>
                <Select
                  items={providers.map((option) => ({
                    value: option.value,
                    label: option.label,
                  }))}
                  value={field.value || ''}
                  onValueChange={field.onChange}
                >
                  <FormControl>
                    <SelectTrigger>
                      <SelectValue placeholder={t('Select provider')} />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {providers.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />

          {providerOption?.needsApiKey && (
            <FormField
              control={control}
              name={`${name}.api_key`}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Backend API key')}</FormLabel>
                  <FormControl>
                    <Input
                      type='password'
                      autoComplete='new-password'
                      placeholder={t('sk-...')}
                      {...field}
                      value={field.value || ''}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

          {providerOption?.modelPlaceholder && (
            <FormField
              control={control}
              name={`${name}.model`}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {provider === 'zhipu' ? t('Search engine') : t('Model')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      placeholder={providerOption.modelPlaceholder}
                      {...field}
                      value={field.value || ''}
                    />
                  </FormControl>
                  <FormDescription>
                    {provider === 'zhipu'
                      ? t('Zhipu search engine: search_std, search_pro, ...')
                      : t('Empty uses the provider default.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

          <FormField
            control={control}
            name={`${name}.api_base`}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Backend API base')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={providerOption?.apiBasePlaceholder}
                    {...field}
                    value={field.value || ''}
                  />
                </FormControl>
                <FormDescription>
                  {provider === 'http_json'
                    ? t(
                        'URL template; {query} is replaced with the search query.'
                      )
                    : t('Empty uses the provider default.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {provider === 'http_json' && (
            <>
              <FormField
                control={control}
                name={`${name}.http_json_result_path`}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Result path')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder='results.#.title'
                        {...field}
                        value={field.value || ''}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'gjson path to the result array (objects with title/url/snippet fields) or a plain string field.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={control}
                name={`${name}.http_json_request_body`}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Request body (optional)')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder='{"q":"{query}","count":5}'
                        {...field}
                        value={field.value || ''}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'JSON body template with {query}; the request becomes POST when set.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </>
          )}
        </>
      )}
    </div>
  )
}

export { SEARCH_PROVIDERS, IMAGE_PROVIDERS }
