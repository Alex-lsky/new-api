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

// Normalization is only used for search filtering and match suggestions.
// Stored pricing keys and billing lookups always use the exact model name.

export function normalizeForModelMatch(name: string): string {
  return name.toLowerCase().replaceAll(/[\s_-]/g, '')
}

export function isFreeModelName(name: string): boolean {
  return normalizeForModelMatch(name).includes('free')
}

// Finds the best priced model for an unset model using bidirectional
// normalized containment ("Gemini 3.7 Flash" <-> "gemini-3.7-flash",
// "gemini-3.7-flash-high" contains "gemini-3.7-flash"). Exact normalized
// equality wins, otherwise the longest normalized candidate. Ambiguous ties
// return '' so the caller falls back to manual selection.
export function findContainmentMatch(
  unsetName: string,
  pricedNames: string[]
): string {
  const target = normalizeForModelMatch(unsetName)
  if (!target) return ''

  const exact = pricedNames.filter(
    (name) => normalizeForModelMatch(name) === target
  )
  if (exact.length === 1) return exact[0]
  if (exact.length > 1) return ''

  const containing = pricedNames.filter((name) => {
    const normalized = normalizeForModelMatch(name)
    if (!normalized) return false
    return normalized.includes(target) || target.includes(normalized)
  })
  if (containing.length === 0) return ''

  const maxNormalizedLength = Math.max(
    ...containing.map((name) => normalizeForModelMatch(name).length)
  )
  const longest = containing.filter(
    (name) => normalizeForModelMatch(name).length === maxNormalizedLength
  )
  return longest.length === 1 ? longest[0] : ''
}

export type SmartMatchAction =
  | { kind: 'alias'; target: string }
  | { kind: 'free' }
  | { kind: 'manual' }

export function buildSmartMatchAction(
  unsetName: string,
  pricedNames: string[]
): SmartMatchAction {
  if (isFreeModelName(unsetName)) {
    return { kind: 'free' }
  }
  const target = findContainmentMatch(unsetName, pricedNames)
  if (target) {
    return { kind: 'alias', target }
  }
  return { kind: 'manual' }
}
