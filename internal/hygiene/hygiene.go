// Package hygiene implements the secret scrubbing pipeline.
// Pipeline order is non-negotiable: scrub before encrypt.
// Secrets must never be stored even in encrypted form.
package hygiene
