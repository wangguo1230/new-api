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
import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const TIER_RATIOS_KEY = 'image_tier_price_setting.tier_ratios'
const MODELS_KEY = 'image_tier_price_setting.models'

// 固定三档，顺序展示
const TIERS = ['1K', '2K', '4K'] as const
type Tier = (typeof TIERS)[number]

const DEFAULT_RATIOS: Record<Tier, number> = { '1K': 1, '2K': 1.5, '4K': 2 }

type ModelRow = { id: number; value: string }

function parseRatios(raw: string | undefined): Record<Tier, number> {
  const result: Record<Tier, number> = { ...DEFAULT_RATIOS }
  if (!raw) return result
  try {
    const parsed = JSON.parse(raw) as unknown
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      for (const tier of TIERS) {
        const v = (parsed as Record<string, unknown>)[tier]
        if (typeof v === 'number' && Number.isFinite(v)) result[tier] = v
      }
    }
  } catch {
    // keep defaults
  }
  return result
}

function parseModels(raw: string | undefined): string[] {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw) as unknown
    if (Array.isArray(parsed)) {
      return parsed.filter((m): m is string => typeof m === 'string')
    }
  } catch {
    // ignore
  }
  return []
}

type ImageTierSettingsProps = {
  tierRatiosDefault: string
  modelsDefault: string
}

export const ImageTierSettings = memo(function ImageTierSettings({
  tierRatiosDefault,
  modelsDefault,
}: ImageTierSettingsProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [ratios, setRatios] = useState<Record<Tier, number>>(DEFAULT_RATIOS)
  const [rows, setRows] = useState<ModelRow[]>([])
  const [nextRowId, setNextRowId] = useState(1)

  useEffect(() => {
    const initialModels = parseModels(modelsDefault)
    const initialRows = initialModels.map((value, index) => ({
      id: index + 1,
      value,
    }))
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setRatios(parseRatios(tierRatiosDefault))
    setRows(initialRows)
    setNextRowId(initialRows.length + 1)
  }, [tierRatiosDefault, modelsDefault])

  const models = useMemo(
    () =>
      rows
        .map((r) => r.value.trim())
        .filter((v, index, arr) => v !== '' && arr.indexOf(v) === index),
    [rows]
  )

  const updateRatio = useCallback((tier: Tier, value: number) => {
    setRatios((prev) => ({ ...prev, [tier]: value }))
  }, [])

  const updateRow = useCallback((id: number, value: string) => {
    setRows((prev) => prev.map((r) => (r.id === id ? { ...r, value } : r)))
  }, [])

  const addRow = useCallback(() => {
    setRows((prev) => [...prev, { id: nextRowId, value: '' }])
    setNextRowId((prev) => prev + 1)
  }, [nextRowId])

  const removeRow = useCallback((id: number) => {
    setRows((prev) => prev.filter((r) => r.id !== id))
  }, [])

  const resetToDefault = useCallback(() => {
    setRatios({ ...DEFAULT_RATIOS })
  }, [])

  const handleSave = useCallback(async () => {
    await updateOption.mutateAsync({
      key: TIER_RATIOS_KEY,
      value: JSON.stringify(ratios),
    })
    await updateOption.mutateAsync({
      key: MODELS_KEY,
      value: JSON.stringify(models),
    })
  }, [models, ratios, updateOption])

  return (
    <SettingsSection title={t('Image Tier Pricing')}>
      <div className='space-y-4'>
        <Alert>
          <AlertDescription className='space-y-1 text-sm'>
            <div>
              {t(
                'For whitelisted image models, billing = base price × tier ratio × n × group ratio. Tier is derived from the image size by its longest edge: ≤1024 → 1K, ≤2048 → 2K, >2048 → 4K.'
              )}
            </div>
            <div>
              {t(
                'Whitelist supports exact names or prefix wildcards, e.g. seedream-4.0 or flux*. An empty whitelist affects no models.'
              )}
            </div>
          </AlertDescription>
        </Alert>

        <div>
          <div className='mb-2 text-sm font-medium'>{t('Tier ratios')}</div>
          <div className='grid gap-4 sm:grid-cols-3'>
            {TIERS.map((tier) => (
              <div key={tier} className='space-y-1'>
                <label className='text-muted-foreground text-xs'>{tier}</label>
                <Input
                  type='number'
                  min={0}
                  step={0.1}
                  value={ratios[tier]}
                  onChange={(e) =>
                    updateRatio(tier, Number(e.target.value) || 0)
                  }
                />
              </div>
            ))}
          </div>
          <div className='mt-2'>
            <Button variant='ghost' size='sm' onClick={resetToDefault}>
              {t('Restore defaults')}
            </Button>
          </div>
        </div>

        <div>
          <div className='mb-2 flex items-center justify-between'>
            <span className='text-sm font-medium'>
              {t('Whitelisted models')}
            </span>
            <Button variant='outline' size='sm' onClick={addRow}>
              <Plus className='mr-2 h-4 w-4' />
              {t('Add')}
            </Button>
          </div>
          {rows.length === 0 ? (
            <p className='text-muted-foreground py-4 text-center text-sm'>
              {t('No models configured')}
            </p>
          ) : (
            <div className='space-y-2'>
              {rows.map((row) => (
                <div key={row.id} className='flex items-center gap-2'>
                  <Input
                    value={row.value}
                    placeholder='seedream-4.0  /  flux*'
                    onChange={(e) => updateRow(row.id, e.target.value)}
                  />
                  <Button
                    variant='ghost'
                    size='icon'
                    onClick={() => removeRow(row.id)}
                    aria-label={t('Delete')}
                  >
                    <Trash2 className='text-destructive h-4 w-4' />
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className='flex justify-end'>
          <Button onClick={handleSave} disabled={updateOption.isPending}>
            {t('Save image tier pricing')}
          </Button>
        </div>
      </div>
    </SettingsSection>
  )
})
