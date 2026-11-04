// Visible state of the dev-mode step-up fallback (see webauthn.ts). Once a
// step-up has been skipped, App shows a banner for the rest of the page's life.

class DevMode {
  stepUpSkipped = $state(false);
}

export const devMode = new DevMode();
