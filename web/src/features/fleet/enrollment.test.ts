import { describe, expect, it } from 'vitest'
import {
  ENROLLMENT_COMMAND_PLACEHOLDER,
  enrollmentCommand,
  formatTokenUses,
} from '@/features/fleet/enrollment'

describe('enrollmentCommand', () => {
  it('builds the compose one-liner around the secret', () => {
    expect(enrollmentCommand('obt_secret123')).toBe(
      'OTEL_FLEET_BOOTSTRAP_TOKEN=obt_secret123 docker compose --profile edge up -d edge-agent',
    )
  })

  it('uses a placeholder token in the empty-state variant', () => {
    expect(ENROLLMENT_COMMAND_PLACEHOLDER).toBe(
      'OTEL_FLEET_BOOTSTRAP_TOKEN=<token> docker compose --profile edge up -d edge-agent',
    )
  })
})

describe('formatTokenUses', () => {
  it('renders unlimited tokens with an infinity sign', () => {
    expect(formatTokenUses(3, 0)).toBe('3 / ∞')
  })

  it('renders bounded tokens as used / max', () => {
    expect(formatTokenUses(2, 5)).toBe('2 / 5')
    expect(formatTokenUses(0, 1)).toBe('0 / 1')
  })
})
