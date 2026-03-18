## Summary

<!-- 1-3 bullet points describing what this PR does and why -->

-

## Linked Docs

<!-- Reference the FEAT/ADR doc this PR implements. Delete rows that don't apply. -->

- Feature: `docs/features/FEAT-XXXX-....md`
- ADR: `docs/adrs/ADR-XXX-....md`

## Changes

<!-- Brief description of the technical approach. What was added/changed/removed? -->

## Test Plan

<!-- How was this tested? Include commands run, test output, or manual verification steps. -->

- [ ] `make test` passes
- [ ] `make lint` clean

## Checklist

- [ ] Feature doc exists and is up to date
- [ ] No secrets or credentials in committed files
- [ ] Error wrapping follows `fmt.Errorf("package: ...: %w", err)` convention
- [ ] `--json` output supported (if adding a CLI command)
- [ ] Table-driven tests, stdlib `testing` only
