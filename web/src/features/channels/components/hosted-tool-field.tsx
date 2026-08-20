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

export interface GlobalProviderOption {
  name: string
}

/**
 * Per-hosted-tool channel card: how this channel handles the tool and, when the
 * gateway executes it, which named global tool provider to run.
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
  providers: GlobalProviderOption[]
}) {
  const { t } = useTranslation()
  const action = useWatch({ control, name: `${name}.action` })
  const emulating = action === 'emulate'

  const actionItems = [
    { value: 'none', label: t('Leave to upstream') },
    { value: 'strip', label: t('Strip from requests') },
    { value: 'emulate', label: t('Gateway executes (emulate)') },
  ]

  const providerItems = [
    { value: '', label: t('Select a provider') },
    ...providers.map((provider) => ({
      value: provider.name,
      label: provider.name,
    })),
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
                    'How this channel treats the hosted web_search tool. Gateway executes runs a provider defined in System Settings → Tool Hosting and restores native web_search_call items.'
                  )
                : t(
                    'How this channel treats the hosted image_generation tool. Gateway executes runs a provider defined in System Settings → Tool Hosting and restores native image_generation_call items.'
                  )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      {emulating && (
        <FormField
          control={control}
          name={`${name}.provider_ref`}
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Tool provider')}</FormLabel>
              <Select
                items={providerItems}
                value={field.value || ''}
                onValueChange={field.onChange}
              >
                <FormControl>
                  <SelectTrigger>
                    <SelectValue placeholder={t('Select a provider')} />
                  </SelectTrigger>
                </FormControl>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {providerItems.map((item) => (
                      <SelectItem key={item.value} value={item.value}>
                        {item.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <FormDescription>
                {t(
                  'The provider is defined once in System Settings → Tool Hosting; the channel just picks which one to use.'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
    </div>
  )
}
