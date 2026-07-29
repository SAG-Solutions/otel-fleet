import { describe, expect, it } from 'vitest'
import { deriveRedirectUri } from '@/features/settings/redirect-uri'

describe('deriveRedirectUri', () => {
  it('builds origin + /auth/{slug}/callback', () => {
    expect(deriveRedirectUri('google', 'https://otel-fleet.example.com')).toBe(
      'https://otel-fleet.example.com/auth/google/callback',
    )
  })

  it('keeps ports and hyphenated slugs intact', () => {
    expect(deriveRedirectUri('corp-sso', 'http://localhost:5173')).toBe(
      'http://localhost:5173/auth/corp-sso/callback',
    )
  })
})
