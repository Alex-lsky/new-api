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
import { Plus } from 'lucide-react'
import { memo, useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { getChannels } from '@/features/channels/api'

import { SettingsPageFormActions } from '../components/settings-page-context'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  EXECUTORS,
  TOOL_KINDS,
  channelModels,
  createProviderRow,
  providersFromOptions,
  providersToOption,
  validateProviders,
  type ProviderRow,
} from './tool-hosting-config'
import { ToolHostingProviderCard } from './tool-hosting-provider-card'

const PROVIDERS_OPTION_KEY = 'tool_hosting.providers'

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

  const { data: channelsData } = useQuery({
    queryKey: ['tool-hosting-channels'],
    queryFn: () => getChannels({ p: 1, page_size: 200 }),
    staleTime: 5 * 60 * 1000,
  })
  const channels = useMemo(
    () => channelsData?.data?.items ?? [],
    [channelsData?.data?.items]
  )
  const channelItems = useMemo(
    () =>
      channels.map((channel) => ({
        value: String(channel.id),
        label: `${channel.name} (#${channel.id})`,
      })),
    [channels]
  )
  const modelsByChannel = useMemo(
    () =>
      new Map(
        channels.map((channel) => [
          String(channel.id),
          channelModels(channel.models),
        ])
      ),
    [channels]
  )
  const errors = useMemo(() => validateProviders(providers), [providers])
  const hasErrors = Object.keys(errors).length > 0

  const updateProvider = useCallback(
    (uid: string, patch: Partial<ProviderRow>) => {
      setProviders((previous) =>
        previous.map((provider) =>
          provider.uid === uid ? { ...provider, ...patch } : provider
        )
      )
    },
    []
  )

  const removeProvider = useCallback((uid: string) => {
    setProviders((previous) =>
      previous.filter((provider) => provider.uid !== uid)
    )
  }, [])

  const save = useCallback(() => {
    if (hasErrors) return
    updateOption.mutate({
      key: PROVIDERS_OPTION_KEY,
      value: JSON.stringify(providersToOption(providers)),
    })
  }, [hasErrors, providers, updateOption])

  return (
    <div className='space-y-6'>
      <SettingsPageFormActions
        onSave={save}
        isSaving={updateOption.isPending}
        isSaveDisabled={hasErrors}
        saveLabel='Save tool hosting settings'
        savingLabel='Saving...'
      />

      <Alert>
        <AlertDescription>
          {t(
            "Define named tool providers here. Search / vision / image-generation providers that run through a model (e.g. Google search = a Gemini model) can borrow an existing channel's credentials and pick its model; plain APIs just need an API key. Channels then select which provider substitutes each hosted tool."
          )}
        </AlertDescription>
      </Alert>

      <section className='space-y-4'>
        <div className='flex flex-wrap items-end justify-between gap-3'>
          <div>
            <h3 className='text-sm font-semibold'>{t('Tool providers')}</h3>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Each provider can be reused by any channel that enables gateway tool execution.'
              )}
            </p>
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() =>
              setProviders((previous) => [...previous, createProviderRow()])
            }
          >
            <Plus data-icon='inline-start' aria-hidden='true' />
            {t('Add provider')}
          </Button>
        </div>

        {providers.length === 0 ? (
          <div className='border-border bg-muted/20 flex min-h-40 flex-col items-center justify-center gap-3 rounded-xl border border-dashed p-6 text-center'>
            <div>
              <p className='font-medium'>{t('No providers configured yet.')}</p>
              <p className='text-muted-foreground mt-1 text-sm'>
                {t(
                  'Add a provider, then select it from the hosted-tool settings of a channel.'
                )}
              </p>
            </div>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => setProviders([createProviderRow()])}
            >
              <Plus data-icon='inline-start' aria-hidden='true' />
              {t('Add provider')}
            </Button>
          </div>
        ) : (
          <div className='grid min-w-0 gap-4 xl:grid-cols-2'>
            {providers.map((provider) => (
              <ToolHostingProviderCard
                key={provider.uid}
                provider={provider}
                errors={errors[provider.uid] ?? {}}
                channelItems={channelItems}
                models={modelsByChannel.get(provider.channel_id) ?? []}
                onChange={(patch) => updateProvider(provider.uid, patch)}
                onRemove={() => removeProvider(provider.uid)}
              />
            ))}
          </div>
        )}
      </section>
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
