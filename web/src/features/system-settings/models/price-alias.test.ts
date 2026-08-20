import { describe, expect, it } from 'vitest'

import {
  buildSmartMatchAction,
  findContainmentMatch,
  isFreeModelName,
  normalizeForModelMatch,
} from './price-alias'

describe('normalizeForModelMatch', () => {
  it('lowercases and strips spaces, hyphens and underscores', () => {
    expect(normalizeForModelMatch('Gemini 3.7 Flash')).toBe('gemini3.7flash')
    expect(normalizeForModelMatch('gemini-3.7-flash')).toBe('gemini3.7flash')
    expect(normalizeForModelMatch('gemini_3_7_flash')).toBe('gemini37flash')
  })

  it('keeps dots so 3.7 and 37 stay distinct', () => {
    expect(normalizeForModelMatch('gemini-3.7-flash')).not.toBe(
      normalizeForModelMatch('gemini-37-flash')
    )
  })
})

describe('isFreeModelName', () => {
  it('detects free regardless of case and separators', () => {
    expect(isFreeModelName('qwen-free')).toBe(true)
    expect(isFreeModelName('Qwen FREE Tier')).toBe(true)
    expect(isFreeModelName('deepseek-chat')).toBe(false)
  })
})

describe('findContainmentMatch', () => {
  const priced = ['gemini-3.7-flash', 'gemini-3.7', 'gpt-5', 'sora-2']

  it('matches names equal after normalization', () => {
    expect(findContainmentMatch('Gemini 3.7 Flash', priced)).toBe(
      'gemini-3.7-flash'
    )
  })

  it('prefers the longest contained candidate', () => {
    expect(findContainmentMatch('gemini-3.7-flash-high', priced)).toBe(
      'gemini-3.7-flash'
    )
  })

  it('matches reverse containment: priced name contains unset name', () => {
    expect(findContainmentMatch('gpt', priced)).toBe('gpt-5')
  })

  it('returns empty when nothing matches', () => {
    expect(findContainmentMatch('claude-opus-9', priced)).toBe('')
  })

  it('returns empty on ambiguous same-length candidates', () => {
    expect(
      findContainmentMatch('model-x', ['model-x-a', 'model-x-b'])
    ).toBe('')
  })

  it('returns empty when two priced names normalize identically', () => {
    expect(
      findContainmentMatch('Gemini 3.7 Flash', [
        'gemini-3.7-flash',
        'gemini_3.7_flash',
      ])
    ).toBe('')
  })
})

describe('buildSmartMatchAction', () => {
  const priced = ['gemini-3.7-flash', 'gpt-5']

  it('marks free-named models as free first', () => {
    expect(buildSmartMatchAction('gemini-3.7-flash-free', priced)).toEqual({
      kind: 'free',
    })
  })

  it('builds alias actions for containment matches', () => {
    expect(buildSmartMatchAction('Gemini 3.7 Flash', priced)).toEqual({
      kind: 'alias',
      target: 'gemini-3.7-flash',
    })
  })

  it('falls back to manual for unmatched models', () => {
    expect(buildSmartMatchAction('claude-opus-9', priced)).toEqual({
      kind: 'manual',
    })
  })
})
