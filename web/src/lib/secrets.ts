/**
 * Sentinel the API returns in place of stored secret values (pipeline
 * component config fields with format password, etc.). Contract:
 * send the sentinel back unchanged to keep the stored secret; send any
 * other string to replace it. Never invent or trim this value.
 */
export const REDACTED_SENTINEL = '__otel_fleet_redacted__'

export function isRedacted(value: unknown): boolean {
  return value === REDACTED_SENTINEL
}
