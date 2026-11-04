# 0003. Web core in a Capacitor shell

- Status: Accepted
- Date: 2026-11-04

## Context
Customers expect an app-store presence, device attestation (App Attest, Play Integrity) and a device-bound signing key. A PWA cannot provide those. A small team cannot maintain two native codebases. The build sandbox has Node 22 but no Xcode, no Android SDK and no access to crates.io.

## Decision
Ship a Svelte 5 PWA now. Wrap the same web core in a Capacitor shell later, when a staging host with native toolchains exists. Rejected:
- PWA alone: no attestation, no device-bound key, no store presence.
- React Native: a second UI codebase for little gain over a web core.
- Flutter: Dart stack with nothing shared with the web client.
- Kotlin Multiplatform: native UIs per platform, again no shared web core.
- Tauri: mobile support is young, and crates.io is unreachable in the build sandbox.

## Consequences
Good:
- One UI codebase, tested with Playwright in the sandbox.
- Passkeys and accessible forms work in the browser today.

Bad:
- No attestation or device-bound key until the shell exists.
- Native plugin work is deferred to a staging host.
- WebView performance and platform quirks.

## Swap path
Add Capacitor and its plugins around the existing build output. The API and auth contracts do not change. If the WebView proves too limiting, replace the shell only; the Go API stays.

## References
- docs/research/reports/Open source bank platform research.md (section "A web core in a Capacitor shell is the thinnest compliant client"; build order, phase 7)
