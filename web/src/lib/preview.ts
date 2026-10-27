// Preview sample walk (docs/contracts/ui.md §3.15, owner decision D49): the UI shows only the
// first samples; the API's sample_index / sample_count stay unchanged.

/** How many samples the UI walks at most. */
export const PREVIEW_SAMPLE_LIMIT = 5

/** Number of samples the UI walks: min(sample_count, PREVIEW_SAMPLE_LIMIT). */
export function walkedSamples(sampleCount: number): number {
  return Math.min(sampleCount, PREVIEW_SAMPLE_LIMIT)
}
