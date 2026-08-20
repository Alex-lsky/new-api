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

For commercial licensing, contact support@quantumnous.com
*/
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge } from '@/components/status-badge'
import { Checkbox } from '@/components/ui/checkbox'

import type { SmartMatchAction } from './price-alias'

export type SmartMatchPlanItem = {
  model: string
  action: SmartMatchAction
}

type SmartMatchDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  items: SmartMatchPlanItem[]
  onApply: (selected: SmartMatchPlanItem[]) => void
}

export function SmartMatchDialog({
  open,
  onOpenChange,
  items,
  onApply,
}: SmartMatchDialogProps) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState<Record<string, boolean>>({})

  useEffect(() => {
    if (!open) return
    setSelected(
      Object.fromEntries(
        items
          .filter((item) => item.action.kind !== 'manual')
          .map((item) => [item.model, true])
      )
    )
  }, [open, items])

  const applicable = items.filter((item) => item.action.kind !== 'manual')
  const selectedItems = applicable.filter((item) => selected[item.model])

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Smart Match')}
      desc={t(
        'Matches models on the current page against priced models, ignoring case, spaces and hyphens. Names containing "free" are set to free.'
      )}
      confirmText={t('Apply {{count}} matches', { count: selectedItems.length })}
      disabled={selectedItems.length === 0}
      handleConfirm={() => onApply(selectedItems)}
      className='max-w-2xl'
    >
      <div className='max-h-[50vh] overflow-y-auto rounded-md border'>
        <table className='w-full text-sm'>
          <tbody className='divide-y'>
            {items.map((item) => {
              const isManual = item.action.kind === 'manual'
              const checked = Boolean(selected[item.model]) && !isManual
              return (
                <tr key={item.model} className={isManual ? 'opacity-60' : ''}>
                  <td className='w-10 px-3 py-2'>
                    <Checkbox
                      checked={checked}
                      disabled={isManual}
                      onCheckedChange={(value) =>
                        setSelected((prev) => ({
                          ...prev,
                          [item.model]: value === true,
                        }))
                      }
                      aria-label={item.model}
                    />
                  </td>
                  <td className='min-w-0 max-w-[220px] truncate px-3 py-2 font-medium'>
                    {item.model}
                  </td>
                  <td className='px-3 py-2'>
                    {item.action.kind === 'alias' && (
                      <span className='flex min-w-0 items-center gap-2'>
                        <StatusBadge
                          label={t('Alias')}
                          variant='info'
                          copyable={false}
                        />
                        <span className='truncate font-mono text-xs'>
                          {item.action.target}
                        </span>
                      </span>
                    )}
                    {item.action.kind === 'free' && (
                      <StatusBadge
                        label={t('Set as free')}
                        variant='success'
                        copyable={false}
                      />
                    )}
                    {isManual && (
                      <StatusBadge
                        label={t('Needs manual match')}
                        variant='neutral'
                        copyable={false}
                      />
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </ConfirmDialog>
  )
}
