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
import { ChevronDown, Trash2 } from 'lucide-react'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'

import {
  EXECUTOR_CONFIGS,
  TOOL_KINDS,
  getExecutorConfig,
  getExecutorDefaultApiBase,
  isToolKind,
  type CredentialSource,
  type ProviderErrors,
  type ProviderRow,
} from './tool-hosting-config'

type SelectOption = {
  value: string
  label: string
}

type ToolHostingProviderCardProps = {
  provider: ProviderRow
  errors: ProviderErrors
  channelItems: SelectOption[]
  models: string[]
  onChange: (patch: Partial<ProviderRow>) => void
  onRemove: () => void
}

function fieldId(prefix: string, uid: string, field: string): string {
  return `${prefix}-${uid}-${field}`
}

export function ToolHostingProviderCard(props: ToolHostingProviderCardProps) {
  const { t } = useTranslation()
  const idPrefix = useId().replaceAll(':', '')
  const executor = getExecutorConfig(props.provider.kind, props.provider.type)
  const hasAdvancedError = Boolean(
    props.errors.api_base ||
    props.errors.request_body ||
    props.errors.result_path
  )
  const [advancedOpen, setAdvancedOpen] = useState(
    props.provider.type === 'http_json' || hasAdvancedError
  )

  useEffect(() => {
    if (props.provider.type === 'http_json' || hasAdvancedError) {
      setAdvancedOpen(true)
    }
  }, [props.provider.type, hasAdvancedError])

  const kindOptions: SelectOption[] = TOOL_KINDS.map((kind) => ({
    value: kind,
    label: t(kind),
  }))
  if (!isToolKind(props.provider.kind)) {
    kindOptions.push({
      value: props.provider.kind,
      label: t('Invalid value: {{value}}', { value: props.provider.kind }),
    })
  }

  const executorOptions = isToolKind(props.provider.kind)
    ? EXECUTOR_CONFIGS[props.provider.kind].map((config) => ({
        value: config.type,
        label: t(config.label),
      }))
    : []
  if (
    props.provider.type &&
    !executorOptions.some((option) => option.value === props.provider.type)
  ) {
    executorOptions.push({
      value: props.provider.type,
      label: t('Invalid value: {{value}}', { value: props.provider.type }),
    })
  }

  const cardTitle = props.provider.name.trim() || t('New tool provider')
  const modeLabel =
    executor?.supportsChannel && props.provider.credentials === 'channel'
      ? t('Use existing channel')
      : t('Direct API')
  const channelBacked =
    executor?.supportsChannel && props.provider.credentials === 'channel'
  const showManualFields = !channelBacked
  const channelRequiresModel = executor?.channelRequiresModel !== false
  const channelUsesKeyOnly = executor?.channelCredentialScope === 'key_only'
  const apiBaseIsPrimary = Boolean(executor?.requiresApiBase)
  const showAdvanced =
    Boolean(executor) &&
    (props.provider.type === 'http_json' ||
      (showManualFields &&
        !apiBaseIsPrimary &&
        (executor?.supportsApiBase !== false ||
          Boolean(executor?.defaultApiBase))) ||
      (channelBacked && channelUsesKeyOnly && executor?.supportsApiBase))

  const handleKindChange = (value: string | null) => {
    const nextKind = value ?? 'web_search'
    if (!isToolKind(nextKind)) return
    const nextExecutor = EXECUTOR_CONFIGS[nextKind][0]
    const credentials = nextExecutor.supportsChannel ? 'channel' : 'manual'
    props.onChange({
      kind: nextKind,
      type: nextExecutor.type,
      credentials,
      channel_id: '',
      model: credentials === 'channel' ? '' : nextExecutor.defaultModel,
      api_key: '',
      api_base: getExecutorDefaultApiBase(nextExecutor, credentials),
      request_body: '',
      result_path: '',
    })
  }

  const handleExecutorChange = (value: string | null) => {
    if (!value || !isToolKind(props.provider.kind)) return
    const nextExecutor = getExecutorConfig(props.provider.kind, value)
    if (!nextExecutor) return
    const credentials = nextExecutor.supportsChannel ? 'channel' : 'manual'
    props.onChange({
      type: value,
      credentials,
      channel_id: '',
      model: credentials === 'channel' ? '' : nextExecutor.defaultModel,
      api_key: '',
      api_base: getExecutorDefaultApiBase(nextExecutor, credentials),
      request_body: '',
      result_path: '',
    })
  }

  const handleCredentialsChange = (value: string | null) => {
    const credentials = (value ?? 'manual') as CredentialSource
    if (!executor) return
    props.onChange({
      credentials,
      channel_id: '',
      model: credentials === 'channel' ? '' : executor.defaultModel,
      api_base: getExecutorDefaultApiBase(executor, credentials),
    })
  }

  const nameId = fieldId(idPrefix, props.provider.uid, 'name')
  const kindId = fieldId(idPrefix, props.provider.uid, 'kind')
  const executorId = fieldId(idPrefix, props.provider.uid, 'executor')
  const credentialsId = fieldId(idPrefix, props.provider.uid, 'credentials')
  const channelId = fieldId(idPrefix, props.provider.uid, 'channel')
  const modelId = fieldId(idPrefix, props.provider.uid, 'model')
  const apiKeyId = fieldId(idPrefix, props.provider.uid, 'api-key')
  const apiBaseId = fieldId(idPrefix, props.provider.uid, 'api-base')
  const resultPathId = fieldId(idPrefix, props.provider.uid, 'result-path')
  const requestBodyId = fieldId(idPrefix, props.provider.uid, 'request-body')

  let apiBaseDescription = t('Enter the address of your self-hosted service.')
  if (executor?.defaultApiBase) {
    apiBaseDescription = t(
      'The official API address is filled automatically. Change it only when using a proxy or compatible endpoint.'
    )
  } else if (props.provider.type === 'http_json') {
    apiBaseDescription = t(
      'Use {query} where the search query should be inserted.'
    )
  }

  const renderApiBaseField = () => (
    <Field data-invalid={Boolean(props.errors.api_base)}>
      <FieldLabel htmlFor={apiBaseId}>
        {props.provider.type === 'http_json' ? t('Request URL') : t('API base')}
      </FieldLabel>
      <Input
        id={apiBaseId}
        value={props.provider.api_base}
        placeholder={
          props.provider.type === 'http_json'
            ? 'https://example.com/search?q={query}'
            : t('Enter service endpoint')
        }
        aria-invalid={Boolean(props.errors.api_base)}
        onChange={(event) => props.onChange({ api_base: event.target.value })}
      />
      <FieldDescription>{apiBaseDescription}</FieldDescription>
      <FieldError>
        {props.errors.api_base && t(props.errors.api_base)}
      </FieldError>
    </Field>
  )

  return (
    <Card
      size='sm'
      data-testid='tool-provider-card'
      data-invalid={Object.keys(props.errors).length > 0}
      className='data-[invalid=true]:ring-destructive/50'
    >
      <CardHeader className='border-b'>
        <CardTitle className='min-w-0 truncate'>{cardTitle}</CardTitle>
        <CardDescription className='flex flex-wrap gap-1.5 pt-1'>
          <StatusBadge
            label={t(props.provider.kind)}
            variant='light-blue'
            copyable={false}
          />
          <StatusBadge
            label={executor ? t(executor.label) : props.provider.type}
            variant='neutral'
            copyable={false}
          />
          {executor && (
            <StatusBadge
              label={modeLabel}
              variant={
                props.provider.credentials === 'channel' ? 'green' : 'grey'
              }
              copyable={false}
            />
          )}
        </CardDescription>
        <CardAction>
          <Button
            type='button'
            variant='ghost'
            size='icon-sm'
            aria-label={t('Remove provider')}
            onClick={props.onRemove}
          >
            <Trash2 aria-hidden='true' />
          </Button>
        </CardAction>
      </CardHeader>

      <CardContent className='space-y-5'>
        <section className='space-y-3'>
          <div>
            <h4 className='text-sm font-semibold'>{t('Basic information')}</h4>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Name the provider and choose the tool and executor it serves.'
              )}
            </p>
          </div>
          <div className='grid gap-4 sm:grid-cols-2'>
            <Field
              className='sm:col-span-2'
              data-invalid={Boolean(props.errors.name)}
            >
              <FieldLabel htmlFor={nameId}>{t('Provider name')}</FieldLabel>
              <Input
                id={nameId}
                value={props.provider.name}
                placeholder='google-search'
                aria-invalid={Boolean(props.errors.name)}
                onChange={(event) =>
                  props.onChange({ name: event.target.value })
                }
              />
              <FieldDescription>
                {t(
                  'Channels use this unique name when selecting a tool provider.'
                )}
              </FieldDescription>
              <FieldError>
                {props.errors.name && t(props.errors.name)}
              </FieldError>
            </Field>

            <Field data-invalid={Boolean(props.errors.kind)}>
              <FieldLabel htmlFor={kindId}>{t('Tool type')}</FieldLabel>
              <Select
                items={kindOptions}
                value={props.provider.kind}
                onValueChange={handleKindChange}
              >
                <SelectTrigger
                  id={kindId}
                  aria-invalid={Boolean(props.errors.kind)}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {kindOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <FieldError>
                {props.errors.kind && t(props.errors.kind)}
              </FieldError>
            </Field>

            <Field data-invalid={Boolean(props.errors.type)}>
              <FieldLabel htmlFor={executorId}>{t('Executor')}</FieldLabel>
              <Select
                items={executorOptions}
                value={props.provider.type}
                onValueChange={handleExecutorChange}
              >
                <SelectTrigger
                  id={executorId}
                  aria-invalid={Boolean(props.errors.type)}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {executorOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <FieldDescription>
                {t('The protocol used by the gateway to execute this tool.')}
              </FieldDescription>
              <FieldError>
                {props.errors.type && t(props.errors.type)}
              </FieldError>
            </Field>
          </div>
        </section>

        {executor && (
          <section className='space-y-3 border-t pt-4'>
            <div>
              <h4 className='text-sm font-semibold'>
                {t('Execution configuration')}
              </h4>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Configure where the gateway gets credentials and runs the tool.'
                )}
              </p>
            </div>

            {executor.supportsChannel && (
              <Field>
                <FieldLabel htmlFor={credentialsId}>
                  {t('Credential source')}
                </FieldLabel>
                <Select
                  items={[
                    {
                      value: 'channel',
                      label: t('Use existing channel'),
                    },
                    { value: 'manual', label: t('Direct API') },
                  ]}
                  value={props.provider.credentials}
                  onValueChange={handleCredentialsChange}
                >
                  <SelectTrigger id={credentialsId}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='channel'>
                        {t('Use existing channel')}
                      </SelectItem>
                      <SelectItem value='manual'>{t('Direct API')}</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FieldDescription>
                  {props.provider.credentials === 'channel'
                    ? t(
                        channelUsesKeyOnly
                          ? 'Borrow only the selected channel key and key rotation settings; this provider keeps its own executor endpoint.'
                          : 'Borrow the selected channel credentials, endpoint, and key rotation settings.'
                      )
                    : t(
                        'Store a dedicated API key, model, and endpoint for this provider.'
                      )}
                </FieldDescription>
              </Field>
            )}

            {channelBacked ? (
              <div className='grid gap-4 sm:grid-cols-2'>
                <Field
                  className={channelRequiresModel ? undefined : 'sm:col-span-2'}
                  data-invalid={Boolean(props.errors.channel_id)}
                >
                  <FieldLabel htmlFor={channelId}>{t('Channel')}</FieldLabel>
                  <Select
                    items={props.channelItems}
                    value={props.provider.channel_id}
                    onValueChange={(value) =>
                      props.onChange({
                        channel_id: value ?? '',
                        model: '',
                      })
                    }
                  >
                    <SelectTrigger
                      id={channelId}
                      aria-invalid={Boolean(props.errors.channel_id)}
                    >
                      <SelectValue placeholder={t('Select channel')} />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {props.channelItems.map((option) => (
                          <SelectItem key={option.value} value={option.value}>
                            {option.label}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FieldError>
                    {props.errors.channel_id && t(props.errors.channel_id)}
                  </FieldError>
                </Field>

                {channelRequiresModel && (
                  <Field data-invalid={Boolean(props.errors.model)}>
                    <FieldLabel htmlFor={modelId}>{t('Model')}</FieldLabel>
                    <Select
                      items={props.models.map((model) => ({
                        value: model,
                        label: model,
                      }))}
                      value={props.provider.model}
                      onValueChange={(value) =>
                        props.onChange({ model: value ?? '' })
                      }
                    >
                      <SelectTrigger
                        id={modelId}
                        aria-invalid={Boolean(props.errors.model)}
                      >
                        <SelectValue placeholder={t('Select model')} />
                      </SelectTrigger>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {props.models.map((model) => (
                            <SelectItem key={model} value={model}>
                              {model}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      {t(
                        'This model executes the tool; it does not replace the client model.'
                      )}
                    </FieldDescription>
                    <FieldError>
                      {props.errors.model && t(props.errors.model)}
                    </FieldError>
                  </Field>
                )}
              </div>
            ) : (
              <div className='grid gap-4 sm:grid-cols-2'>
                {executor.requiresApiKey && (
                  <Field data-invalid={Boolean(props.errors.api_key)}>
                    <FieldLabel htmlFor={apiKeyId}>{t('API key')}</FieldLabel>
                    <Input
                      id={apiKeyId}
                      type='password'
                      autoComplete='new-password'
                      value={props.provider.api_key}
                      aria-invalid={Boolean(props.errors.api_key)}
                      onChange={(event) =>
                        props.onChange({ api_key: event.target.value })
                      }
                    />
                    <FieldError>
                      {props.errors.api_key && t(props.errors.api_key)}
                    </FieldError>
                  </Field>
                )}

                {executor.requiresModel && (
                  <Field data-invalid={Boolean(props.errors.model)}>
                    <FieldLabel htmlFor={modelId}>
                      {t(executor.modelLabel)}
                    </FieldLabel>
                    <Input
                      id={modelId}
                      value={props.provider.model}
                      placeholder={executor.defaultModel}
                      aria-invalid={Boolean(props.errors.model)}
                      onChange={(event) =>
                        props.onChange({ model: event.target.value })
                      }
                    />
                    {executor.modelLabel === 'Search engine' && (
                      <FieldDescription>
                        {t(
                          'This selects the Zhipu search engine, not a chat model or MCP node.'
                        )}
                      </FieldDescription>
                    )}
                    <FieldError>
                      {props.errors.model && t(props.errors.model)}
                    </FieldError>
                  </Field>
                )}

                {apiBaseIsPrimary && (
                  <div className='sm:col-span-2'>{renderApiBaseField()}</div>
                )}
              </div>
            )}
          </section>
        )}

        {showAdvanced && (
          <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
            <CollapsibleTrigger
              render={
                <button
                  type='button'
                  className='hover:bg-muted/40 flex w-full items-center justify-between rounded-lg border px-3 py-3 text-left transition-colors'
                  aria-expanded={advancedOpen}
                />
              }
            >
              <div>
                <div className='text-sm font-semibold'>
                  {t('Advanced Settings')}
                </div>
                <div className='text-muted-foreground text-xs'>
                  {props.provider.type === 'http_json'
                    ? t('Configure the custom request and response mapping.')
                    : t(
                        'Override the prefilled official API address only when needed.'
                      )}
                </div>
              </div>
              <ChevronDown
                className={cn(
                  'text-muted-foreground size-4 transition-transform',
                  advancedOpen && 'rotate-180'
                )}
                aria-hidden='true'
              />
            </CollapsibleTrigger>
            <CollapsibleContent className='space-y-4 pt-4'>
              {!apiBaseIsPrimary &&
                (showManualFields || channelUsesKeyOnly) &&
                renderApiBaseField()}
              {props.provider.type === 'http_json' && (
                <>
                  <Field data-invalid={Boolean(props.errors.result_path)}>
                    <FieldLabel htmlFor={resultPathId}>
                      {t('Result path')}
                    </FieldLabel>
                    <Input
                      id={resultPathId}
                      value={props.provider.result_path}
                      placeholder='data.results'
                      aria-invalid={Boolean(props.errors.result_path)}
                      onChange={(event) =>
                        props.onChange({ result_path: event.target.value })
                      }
                    />
                    <FieldDescription>
                      {t(
                        'GJSON path to the result array or answer string in the response.'
                      )}
                    </FieldDescription>
                    <FieldError>
                      {props.errors.result_path && t(props.errors.result_path)}
                    </FieldError>
                  </Field>

                  <Field data-invalid={Boolean(props.errors.request_body)}>
                    <FieldLabel htmlFor={requestBodyId}>
                      {t('Request body template')}
                    </FieldLabel>
                    <Textarea
                      id={requestBodyId}
                      value={props.provider.request_body}
                      placeholder='{"q":"{query}"}'
                      aria-invalid={Boolean(props.errors.request_body)}
                      onChange={(event) =>
                        props.onChange({ request_body: event.target.value })
                      }
                    />
                    <FieldDescription>
                      {t(
                        'Leave empty for GET. A JSON body containing {query} sends POST instead.'
                      )}
                    </FieldDescription>
                    <FieldError>
                      {props.errors.request_body &&
                        t(props.errors.request_body)}
                    </FieldError>
                  </Field>
                </>
              )}
            </CollapsibleContent>
          </Collapsible>
        )}
      </CardContent>
    </Card>
  )
}
