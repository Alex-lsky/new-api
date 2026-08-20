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
import { useQuery } from '@tanstack/react-query'
import { Trash2 } from 'lucide-react'
import { memo, useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { getChannels } from '@/features/channels/api'

import { useUpdateOption } from '../hooks/use-update-option'

const PROVIDERS_OPTION_KEY = 'tool_hosting.providers'

const TOOL_KINDS = [
  'web_search',
  'image_recognition',
  'image_generation',
] as const

type ToolKind = (typeof TOOL_KINDS)[number]

// Executors valid per tool kind.
const EXECUTORS: Record<ToolKind, string[]> = {
  web_search: [
    'gemini',
    'zhipu',
    'tavily',
    'brave',
    'bocha',
    'searxng',
    'http_json',
  ],
  image_recognition: ['gemini', 'openai'],
  image_generation: ['openai_images', 'gemini_images'],
}

// Executors that run through a model: channel-backed credentials (and a model
// selection) make sense for them — e.g. Google search is really a gemini model.
const MODEL_EXECUTORS = new Set([
  'gemini',
  'openai',
  'openai_images',
  'gemini_images',
  'zhipu', // model field selects the search engine
])

const KEYLESS_EXECUTORS = new Set(['searxng', 'http_json'])

type ProviderRow = {
  uid: string
  name: string
  kind: string
  type: string
  credentials: 'channel' | 'manual'
  channel_id: string
  model: string
  api_key: string
  api_base: string
  request_body: string
  result_path: string
}

type RawProviderOption = {
  kind?: string
  type?: string
  channel_id?: number
  model?: string
  api_key?: string
  api_base?: string
  extra?: Record<string, string>
}

let uidCounter = 0

function nextUid(): string {
  uidCounter += 1
  return `uid-${Date.now().toString(36)}-${uidCounter}`
}

function emptyProvider(kind: ToolKind = 'web_search'): ProviderRow {
  const type = EXECUTORS[kind][0] || 'gemini'
  return {
    uid: nextUid(),
    name: '',
    kind,
    type,
    credentials: MODEL_EXECUTORS.has(type) ? 'channel' : 'manual',
    channel_id: '',
    model: '',
    api_key: '',
    api_base: '',
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

function providersFromOptions(value: string): ProviderRow[] {
  const raw = parseOptionObject<Record<string, RawProviderOption>>(value)
  return Object.entries(raw)
    .filter(([name]) => name.trim())
    .map(([name, provider], index) => ({
      uid: nextUid() + String(index),
      name,
      kind: provider.kind || 'web_search',
      type: provider.type || 'tavily',
      credentials:
        provider.channel_id && provider.channel_id > 0 ? 'channel' : 'manual',
      channel_id: provider.channel_id ? String(provider.channel_id) : '',
      model: provider.model || '',
      api_key: provider.api_key || '',
      api_base: provider.api_base || '',
      request_body: provider.extra?.request_body || '',
      result_path: provider.extra?.result_path || '',
    }))
}

function providerToOption(provider: ProviderRow): Record<string, unknown> {
  const base: Record<string, unknown> = {
    kind: provider.kind,
    type: provider.type,
  }
  if (provider.credentials === 'channel' && provider.channel_id) {
    base.channel_id = Number(provider.channel_id) || 0
    if (provider.model.trim()) base.model = provider.model.trim()
    return base
  }
  if (provider.api_key.trim()) base.api_key = provider.api_key.trim()
  if (provider.model.trim()) base.model = provider.model.trim()
  if (provider.api_base.trim()) base.api_base = provider.api_base.trim()
  if (provider.type === 'http_json') {
    const extra: Record<string, string> = {}
    if (provider.request_body.trim()) {
      extra.request_body = provider.request_body
    }
    if (provider.result_path.trim()) {
      extra.result_path = provider.result_path.trim()
    }
    base.extra = extra
  }
  return base
}

function providersToOption(rows: ProviderRow[]): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  rows.forEach((provider) => {
    if (!provider.name.trim()) return
    out[provider.name.trim()] = providerToOption(provider)
  })
  return out
}

function channelModels(models: string | undefined): string[] {
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

function ToolHostingSettingsCard({
  providers: providersOption,
}: {
  providers: string
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [providers, setProviders] = useState<ProviderRow[]>(() =>
    providersFromOptions(providersOption)
  )
  const [saving, setSaving] = useState(false)

  const { data: channelsData } = useQuery({
    queryKey: ['tool-hosting-channels'],
    queryFn: () => getChannels({ p: 1, page_size: 200 }),
    staleTime: 5 * 60 * 1000,
  })
  const channels = channelsData?.data?.items ?? []
  const channelItems = channels.map((channel) => ({
    value: String(channel.id),
    label: `${channel.name} (#${channel.id})`,
  }))
  const modelsByChannel = new Map(
    channels.map((channel) => [
      String(channel.id),
      channelModels(channel.models),
    ])
  )

  const updateProvider = useCallback(
    (index: number, patch: Partial<ProviderRow>) => {
      setProviders((prev) =>
        prev.map((row, i) => (i === index ? { ...row, ...patch } : row))
      )
    },
    []
  )

  const save = useCallback(async () => {
    setSaving(true)
    try {
      await updateOption.mutateAsync({
        key: PROVIDERS_OPTION_KEY,
        value: JSON.stringify(providersToOption(providers)),
      })
      toast.success(t('Tool hosting settings saved'))
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      toast.error(t('Failed to save tool hosting settings') + ': ' + message)
    } finally {
      setSaving(false)
    }
  }, [providers, updateOption, t])

  return (
    <div className='space-y-6'>
      <Alert>
        <AlertDescription>
          {t(
            "Define named tool providers here. Search / vision / image-generation providers that run through a model (e.g. Google search = a Gemini model) can borrow an existing channel's credentials and pick its model; plain APIs just need an API key. Channels then select which provider substitutes each hosted tool."
          )}
        </AlertDescription>
      </Alert>

      <section className='space-y-3'>
        <h3 className='text-sm font-semibold'>{t('Tool providers')}</h3>
        <div className='space-y-2'>
          {providers.length === 0 && (
            <p className='text-muted-foreground text-sm'>
              {t('No providers configured yet.')}
            </p>
          )}
          {providers.map((provider, index) => {
            const isModelType = MODEL_EXECUTORS.has(provider.type)
            const isKeyless = KEYLESS_EXECUTORS.has(provider.type)
            const models = modelsByChannel.get(provider.channel_id) ?? []
            return (
              <div
                key={provider.uid}
                className='grid grid-cols-12 gap-2 rounded-md border p-2'
              >
                <Field className='col-span-2'>
                  <Input
                    placeholder={t('Name')}
                    value={provider.name}
                    onChange={(event) =>
                      updateProvider(index, { name: event.target.value })
                    }
                  />
                </Field>
                <Field className='col-span-2'>
                  <Select
                    items={TOOL_KINDS.map((kind) => ({
                      value: kind,
                      label: kind,
                    }))}
                    value={provider.kind}
                    onValueChange={(kind) => {
                      const nextKind = (kind ?? 'web_search') as ToolKind
                      const nextType = EXECUTORS[nextKind][0] || 'gemini'
                      updateProvider(index, {
                        kind: nextKind,
                        type: nextType,
                        credentials: MODEL_EXECUTORS.has(nextType)
                          ? 'channel'
                          : 'manual',
                        channel_id: '',
                        model: '',
                      })
                    }}
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {TOOL_KINDS.map((kind) => (
                          <SelectItem key={kind} value={kind}>
                            {kind}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </Field>
                <Field className='col-span-2'>
                  <Select
                    items={EXECUTORS[provider.kind as ToolKind].map((type) => ({
                      value: type,
                      label: type,
                    }))}
                    value={provider.type}
                    onValueChange={(type) => {
                      const next = type ?? ''
                      updateProvider(index, {
                        type: next,
                        credentials: MODEL_EXECUTORS.has(next)
                          ? 'channel'
                          : 'manual',
                        channel_id: '',
                        model: '',
                      })
                    }}
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {EXECUTORS[provider.kind as ToolKind].map((type) => (
                          <SelectItem key={type} value={type}>
                            {type}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </Field>

                {isModelType ? (
                  <>
                    <Field className='col-span-3'>
                      <Select
                        items={[
                          {
                            value: 'channel',
                            label: t('Use existing channel'),
                          },
                          { value: 'manual', label: t('Direct API') },
                        ]}
                        value={provider.credentials}
                        onValueChange={(credentials) =>
                          updateProvider(index, {
                            credentials: (credentials ?? 'manual') as
                              | 'channel'
                              | 'manual',
                            channel_id: '',
                          })
                        }
                      >
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='channel'>
                              {t('Use existing channel')}
                            </SelectItem>
                            <SelectItem value='manual'>
                              {t('Direct API')}
                            </SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </Field>
                    {provider.credentials === 'channel' ? (
                      <>
                        <Field className='col-span-2'>
                          <Select
                            items={channelItems}
                            value={provider.channel_id}
                            onValueChange={(value) =>
                              updateProvider(index, {
                                channel_id: value ?? '',
                                model: '',
                              })
                            }
                          >
                            <SelectTrigger>
                              <SelectValue placeholder={t('Select channel')} />
                            </SelectTrigger>
                            <SelectContent alignItemWithTrigger={false}>
                              <SelectGroup>
                                {channelItems.map((item) => (
                                  <SelectItem
                                    key={item.value}
                                    value={item.value}
                                  >
                                    {item.label}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </Field>
                        <Field className='col-span-2'>
                          <Select
                            items={models.map((model) => ({
                              value: model,
                              label: model,
                            }))}
                            value={provider.model}
                            onValueChange={(value) =>
                              updateProvider(index, { model: value ?? '' })
                            }
                          >
                            <SelectTrigger>
                              <SelectValue placeholder={t('Select model')} />
                            </SelectTrigger>
                            <SelectContent alignItemWithTrigger={false}>
                              <SelectGroup>
                                {models.map((model) => (
                                  <SelectItem key={model} value={model}>
                                    {model}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </Field>
                      </>
                    ) : (
                      <>
                        <Field className='col-span-2'>
                          <Input
                            type='password'
                            autoComplete='new-password'
                            placeholder={t('API key')}
                            value={provider.api_key}
                            onChange={(event) =>
                              updateProvider(index, {
                                api_key: event.target.value,
                              })
                            }
                          />
                        </Field>
                        <Field className='col-span-2'>
                          <Input
                            placeholder={
                              provider.type === 'zhipu'
                                ? 'search_std'
                                : t('Model')
                            }
                            value={provider.model}
                            onChange={(event) =>
                              updateProvider(index, {
                                model: event.target.value,
                              })
                            }
                          />
                        </Field>
                      </>
                    )}
                  </>
                ) : (
                  <>
                    <Field className='col-span-3'>
                      {!isKeyless && (
                        <Input
                          type='password'
                          autoComplete='new-password'
                          placeholder={t('API key')}
                          value={provider.api_key}
                          onChange={(event) =>
                            updateProvider(index, {
                              api_key: event.target.value,
                            })
                          }
                        />
                      )}
                    </Field>
                    <Field className='col-span-2'>
                      {isKeyless || provider.type === 'http_json' ? (
                        <Input
                          placeholder={t('API base')}
                          value={provider.api_base}
                          onChange={(event) =>
                            updateProvider(index, {
                              api_base: event.target.value,
                            })
                          }
                        />
                      ) : null}
                    </Field>
                  </>
                )}

                <Field className='col-span-2'>
                  <Input
                    placeholder={t('API base')}
                    value={provider.api_base}
                    onChange={(event) =>
                      updateProvider(index, { api_base: event.target.value })
                    }
                  />
                </Field>
                {provider.type === 'http_json' && (
                  <>
                    <Field className='col-span-4'>
                      <Input
                        placeholder={t('Result path')}
                        value={provider.result_path}
                        onChange={(event) =>
                          updateProvider(index, {
                            result_path: event.target.value,
                          })
                        }
                      />
                    </Field>
                    <Field className='col-span-4'>
                      <Input
                        placeholder={'{"q":"{query}"}'}
                        value={provider.request_body}
                        onChange={(event) =>
                          updateProvider(index, {
                            request_body: event.target.value,
                          })
                        }
                      />
                    </Field>
                  </>
                )}
                <div className='col-span-1 flex items-start'>
                  <Button
                    variant='ghost'
                    size='icon'
                    aria-label={t('Remove provider')}
                    onClick={() =>
                      setProviders((prev) => prev.filter((_, i) => i !== index))
                    }
                  >
                    <Trash2 className='h-4 w-4' />
                  </Button>
                </div>
              </div>
            )
          })}
          <Button
            variant='outline'
            size='sm'
            onClick={() => setProviders((prev) => [...prev, emptyProvider()])}
          >
            {t('Add provider')}
          </Button>
        </div>
      </section>

      <div>
        <Button onClick={save} disabled={saving}>
          {saving ? t('Saving...') : t('Save tool hosting settings')}
        </Button>
      </div>
    </div>
  )
}

export interface SharedToolProvider {
  name: string
  kind?: string
  type?: string
}

const MemoToolHostingSettingsCard = memo(ToolHostingSettingsCard)
export {
  MemoToolHostingSettingsCard as ToolHostingSettingsCard,
  TOOL_KINDS,
  EXECUTORS,
}
