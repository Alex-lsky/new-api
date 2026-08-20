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
import { Trash2 } from 'lucide-react'
import { memo, useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldError } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { useUpdateOption } from '../hooks/use-update-option'

const PROVIDERS_OPTION_KEY = 'tool_hosting.providers'
const BINDINGS_OPTION_KEY = 'tool_hosting.bindings'

const TOOL_KINDS = [
  'web_search',
  'image_recognition',
  'image_generation',
] as const

type ToolKind = (typeof TOOL_KINDS)[number]

// Standard executors valid per tool kind.
const EXECUTORS: Record<ToolKind, string[]> = {
  web_search: [
    'gemini',
    'zhipu',
    'tavily',
    'brave',
    'bocha',
    'searxng',
    'http_json',
    'channel',
  ],
  image_recognition: ['gemini', 'openai', 'channel'],
  image_generation: ['openai_images', 'gemini_images', 'channel'],
}

const CHANNEL_EXECUTOR_HINT: Record<ToolKind, string[]> = {
  web_search: ['gemini', 'zhipu', 'tavily', 'brave', 'bocha', 'http_json'],
  image_recognition: ['gemini', 'openai'],
  image_generation: ['openai_images', 'gemini_images'],
}

type ProviderRow = {
  uid: string
  name: string
  kind: string
  type: string
  channel_id: string
  executor: string
  api_key: string
  model: string
  api_base: string
  request_body: string
  result_path: string
}

type BindingRow = {
  id: number
  model: string
  web_search: string
  image_recognition: string
  image_generation: string
}

function emptyProvider(): ProviderRow {
  return {
    uid: crypto.randomUUID(),
    name: '',
    kind: 'web_search',
    type: 'tavily',
    channel_id: '',
    executor: '',
    api_key: '',
    model: '',
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

type RawProviderOption = ProviderRow & { extra?: Record<string, string> }

function providersFromOptions(value: string): ProviderRow[] {
  const raw =
    parseOptionObject<Record<string, Partial<RawProviderOption>>>(value)
  return Object.entries(raw)
    .filter(([name]) => name.trim())
    .map(([name, provider], index) => ({
      uid: provider.uid || name || String(index),
      name,
      kind: provider.kind || 'web_search',
      type: provider.type || 'tavily',
      channel_id: provider.channel_id ? String(provider.channel_id) : '',
      executor: provider.executor || '',
      api_key: provider.api_key || '',
      model: provider.model || '',
      api_base: provider.api_base || '',
      request_body: provider.request_body || provider.extra?.request_body || '',
      result_path: provider.result_path || provider.extra?.result_path || '',
    }))
}

function bindingsFromOptions(value: string): BindingRow[] {
  const raw =
    parseOptionObject<Record<string, Partial<Record<ToolKind, string>>>>(value)
  return Object.entries(raw)
    .filter(([model]) => model.trim())
    .map(([model, kinds], index) => ({
      id: index,
      model,
      web_search: kinds.web_search || '',
      image_recognition: kinds.image_recognition || '',
      image_generation: kinds.image_generation || '',
    }))
}

function providerToOption(provider: ProviderRow): Record<string, unknown> {
  const base: Record<string, unknown> = {
    kind: provider.kind,
    type: provider.type,
  }
  if (provider.type === 'channel') {
    base.channel_id = Number(provider.channel_id) || 0
    base.executor = provider.executor
    return base
  }
  if (provider.api_key.trim()) base.api_key = provider.api_key.trim()
  if (provider.model.trim()) base.model = provider.model.trim()
  if (provider.api_base.trim()) base.api_base = provider.api_base.trim()
  if (provider.type === 'http_json') {
    const extra: Record<string, string> = {}
    if (provider.request_body.trim()) extra.request_body = provider.request_body
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

function bindingsToOption(rows: BindingRow[]): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const row of rows) {
    if (!row.model.trim()) continue
    const kinds: Record<string, string> = {}
    for (const kind of TOOL_KINDS) {
      const provider = row[kind]
      if (provider) kinds[kind] = provider
    }
    if (Object.keys(kinds).length > 0) out[row.model.trim()] = kinds
  }
  return out
}

function ToolHostingSettingsCard({
  providers: providersOption,
  bindings: bindingsOption,
}: {
  providers: string
  bindings: string
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [providers, setProviders] = useState<ProviderRow[]>(() =>
    providersFromOptions(providersOption)
  )
  const [bindings, setBindings] = useState<BindingRow[]>(() =>
    bindingsFromOptions(bindingsOption)
  )
  const [saving, setSaving] = useState(false)

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
      const providerValue = JSON.stringify(providersToOption(providers))
      const bindingValue = JSON.stringify(bindingsToOption(bindings))
      await updateOption.mutateAsync({
        key: PROVIDERS_OPTION_KEY,
        value: providerValue,
      })
      await updateOption.mutateAsync({
        key: BINDINGS_OPTION_KEY,
        value: bindingValue,
      })
      toast.success(t('Tool hosting settings saved'))
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      toast.error(t('Failed to save tool hosting settings') + ': ' + message)
    } finally {
      setSaving(false)
    }
  }, [providers, bindings, updateOption, t])

  return (
    <div className='space-y-6'>
      <Alert>
        <AlertDescription>
          {t(
            'Global tool hosting configures named search / vision / image-generation providers and binds them to models. Channels inherit these bindings for tool types they do not pin themselves; the channel card "exclude global" opts a channel out.'
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
          {providers.map((provider, index) => (
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
                  onValueChange={(kind) =>
                    updateProvider(index, {
                      kind: kind ?? '',
                      type:
                        EXECUTORS[(kind ?? 'web_search') as ToolKind][0] ||
                        'channel',
                      executor: '',
                    })
                  }
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
                  onValueChange={(type) =>
                    updateProvider(index, {
                      type: type ?? '',
                      executor: type === 'channel' ? provider.executor : '',
                    })
                  }
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

              {provider.type === 'channel' ? (
                <>
                  <Field className='col-span-2'>
                    <Input
                      type='number'
                      placeholder={t('Channel ID')}
                      value={provider.channel_id}
                      onChange={(event) =>
                        updateProvider(index, {
                          channel_id: event.target.value,
                        })
                      }
                    />
                  </Field>
                  <Field className='col-span-2'>
                    <Select
                      items={(
                        CHANNEL_EXECUTOR_HINT[provider.kind as ToolKind] || []
                      ).map((executor) => ({
                        value: executor,
                        label: executor,
                      }))}
                      value={provider.executor}
                      onValueChange={(executor) =>
                        updateProvider(index, { executor: executor ?? '' })
                      }
                    >
                      <SelectTrigger>
                        <SelectValue placeholder={t('Executor')} />
                      </SelectTrigger>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {(
                            CHANNEL_EXECUTOR_HINT[provider.kind as ToolKind] ||
                            []
                          ).map((executor) => (
                            <SelectItem key={executor} value={executor}>
                              {executor}
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
                        updateProvider(index, { api_key: event.target.value })
                      }
                    />
                  </Field>
                  {provider.kind !== 'web_search' ||
                  !['tavily', 'brave', 'bocha'].includes(provider.type) ? (
                    <Field
                      className={
                        provider.kind === 'image_recognition' &&
                        provider.type === 'openai'
                          ? 'col-span-2'
                          : 'col-span-2'
                      }
                    >
                      <Input
                        placeholder={
                          provider.kind === 'web_search' &&
                          provider.type === 'zhipu'
                            ? 'search_std'
                            : t('Model')
                        }
                        value={provider.model}
                        onChange={(event) =>
                          updateProvider(index, { model: event.target.value })
                        }
                      />
                    </Field>
                  ) : (
                    <div className='col-span-2' />
                  )}
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
          ))}
          <Button
            variant='outline'
            size='sm'
            onClick={() => setProviders((prev) => [...prev, emptyProvider()])}
          >
            {t('Add provider')}
          </Button>
        </div>
      </section>

      <section className='space-y-3'>
        <h3 className='text-sm font-semibold'>{t('Model bindings')}</h3>
        <div className='space-y-2'>
          {bindings.length === 0 && (
            <p className='text-muted-foreground text-sm'>
              {t('No model bindings yet.')}
            </p>
          )}
          {bindings.map((row, index) => (
            <div
              key={row.id}
              className='grid grid-cols-5 gap-2 rounded-md border p-2'
            >
              <Field>
                <Input
                  placeholder={t('Model or prefix*')}
                  value={row.model}
                  onChange={(event) =>
                    setBindings((prev) =>
                      prev.map((r, i) =>
                        i === index ? { ...r, model: event.target.value } : r
                      )
                    )
                  }
                />
              </Field>
              {TOOL_KINDS.map((kind) => (
                <Field key={kind}>
                  <Select
                    items={[
                      { value: '', label: t('Not bound') },
                      ...providers
                        .filter((p) => p.kind === kind && p.name)
                        .map((p) => ({ value: p.name, label: p.name })),
                    ]}
                    value={row[kind] || ''}
                    onValueChange={(value) =>
                      setBindings((prev) =>
                        prev.map((r, i) =>
                          i === index ? { ...r, [kind]: value ?? '' } : r
                        )
                      )
                    }
                  >
                    <SelectTrigger>
                      <SelectValue placeholder={kind} />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {[
                          { value: '', label: t('Not bound') },
                          ...providers
                            .filter((p) => p.kind === kind && p.name)
                            .map((p) => ({ value: p.name, label: p.name })),
                        ].map((item) => (
                            <SelectItem key={item.value} value={item.value}>
                              {item.label}
                            </SelectItem>
                          ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </Field>
              ))}
              <div className='flex items-start justify-end'>
                <Button
                  variant='ghost'
                  size='icon'
                  aria-label={t('Remove binding')}
                  onClick={() =>
                    setBindings((prev) => prev.filter((_, i) => i !== index))
                  }
                >
                  <Trash2 className='h-4 w-4' />
                </Button>
              </div>
            </div>
          ))}
          <Button
            variant='outline'
            size='sm'
            onClick={() =>
              setBindings((prev) => [
                ...prev,
                {
                  id: Date.now(),
                  model: '',
                  web_search: '',
                  image_recognition: '',
                  image_generation: '',
                },
              ])
            }
          >
            {t('Add binding')}
          </Button>
        </div>
      </section>

      <div>
        <Button onClick={save} disabled={saving}>
          {saving ? t('Saving...') : t('Save tool hosting settings')}
        </Button>
      </div>
      {providers.some((p) => p.kind === 'image_recognition') && (
        <FieldError className='text-sm'>
          {t(
            'When image_recognition is bound, the gateway injects it as a callable tool; text-only upstreams can then read the images users attach.'
          )}
        </FieldError>
      )}
    </div>
  )
}

const MemoToolHostingSettingsCard = memo(ToolHostingSettingsCard)
export { MemoToolHostingSettingsCard as ToolHostingSettingsCard }
