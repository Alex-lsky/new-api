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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { updateSystemOption } from '@/features/system-settings/api'
import { useSystemOptions } from '@/features/system-settings/hooks/use-system-options'

const BINDINGS_KEY = 'tool_hosting.bindings'
const TOOL_KINDS = [
  'web_search',
  'image_recognition',
  'image_generation',
] as const

type Kind = (typeof TOOL_KINDS)[number]

function parseBindings(
  value: string
): Record<string, Partial<Record<Kind, string>>> {
  try {
    const parsed = JSON.parse(value || '{}')
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

/**
 * Per-model tool binding editor reached from the model table row actions.
 * Writes the same tool_hosting.bindings option as the system-settings Tool
 * Hosting page.
 */
export function ModelToolBindingsDialog({
  modelName,
  open,
  onOpenChange,
}: {
  modelName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const { data: options } = useSystemOptions()
  const optionMap = options as unknown as Record<string, string | undefined>
  const bindingsValue = optionMap?.[BINDINGS_KEY]
  const providersValue = optionMap?.['tool_hosting.providers']

  const providers = parseBindings(providersValue ?? '{}') as unknown as Record<
    string,
    { kind?: string }
  >
  const existing = parseBindings(bindingsValue ?? '{}')
  const initial = existing[modelName] ?? {}
  const [webSearch, setWebSearch] = useState<string>(initial.web_search || '')
  const [recognition, setRecognition] = useState<string>(
    initial.image_recognition || ''
  )
  const [imageGen, setImageGen] = useState<string>(
    initial.image_generation || ''
  )
  const [saving, setSaving] = useState(false)

  const providerNamesOfKind = (kind: string) =>
    Object.entries(providers)
      .filter(([, provider]) => provider?.kind === kind)
      .map(([name]) => name)

  const save = async () => {
    setSaving(true)
    try {
      const bindings = parseBindings(bindingsValue ?? '{}')
      const next: Partial<Record<Kind, string>> = {}
      if (webSearch) next.web_search = webSearch
      if (recognition) next.image_recognition = recognition
      if (imageGen) next.image_generation = imageGen
      if (modelName.trim()) {
        if (Object.keys(next).length > 0) bindings[modelName] = next
        else delete bindings[modelName]
      }
      await updateSystemOption({
        key: BINDINGS_KEY,
        value: JSON.stringify(bindings),
      })
      toast.success(t('Tool bindings saved for model'))
      onOpenChange(false)
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      toast.error(t('Failed to save tool bindings') + ': ' + message)
    } finally {
      setSaving(false)
    }
  }

  const kindSelect = (
    label: string,
    value: string,
    onChange: (v: string) => void,
    options_: string[]
  ) => (
    <Field>
      <Select
        items={[
          { value: '', label: t('Not bound') },
          ...options_.map((name) => ({ value: name, label: name })),
        ]}
        value={value || ''}
        onValueChange={(v) => onChange(v ?? '')}
      >
        <SelectTrigger aria-label={label}>
          <SelectValue placeholder={label} />
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          <SelectGroup>
            {[
              { value: '', label: t('Not bound') },
              ...options_.map((name) => ({ value: name, label: name })),
            ].map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </Field>
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>
            {t('Tool bindings')} — {modelName}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Choose which global tool provider executes each hosted tool for this model. Channels without a local setting inherit these.'
            )}
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-3'>
          {kindSelect(
            'web_search',
            webSearch,
            setWebSearch,
            providerNamesOfKind('web_search')
          )}
          {kindSelect(
            'image_recognition',
            recognition,
            setRecognition,
            providerNamesOfKind('image_recognition')
          )}
          {kindSelect(
            'image_generation',
            imageGen,
            setImageGen,
            providerNamesOfKind('image_generation')
          )}
        </div>
        <DialogFooter>
          <DialogClose
            render={<Button variant='outline'>{t('Cancel')}</Button>}
          />
          <Button onClick={save} disabled={saving}>
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
