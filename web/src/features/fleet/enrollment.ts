/**
 * Edge-agent enrollment helpers — pure string builders so the snippet shown
 * in the show-once token dialog and the fleet empty state stay in sync.
 */

/** Copy-pasteable enrollment one-liner for the compose dev environment. */
export function enrollmentCommand(secret: string): string {
  return `OTEL_FLEET_BOOTSTRAP_TOKEN=${secret} docker compose --profile edge up -d edge-agent`
}

/** Placeholder variant for empty states where no real secret exists. */
export const ENROLLMENT_COMMAND_PLACEHOLDER = enrollmentCommand('<token>')

/** "3 / ∞" or "3 / 5" — a maxUses of 0 means unlimited. */
export function formatTokenUses(usedCount: number, maxUses: number): string {
  return `${usedCount} / ${maxUses === 0 ? '∞' : maxUses}`
}
