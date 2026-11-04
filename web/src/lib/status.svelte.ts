// One app-wide polite live region. Screens call announce() for outcomes that
// screen-reader users must hear (sent, approved, errors).

class Status {
  message = $state('');
  #timer: ReturnType<typeof setTimeout> | undefined;

  announce(text: string): void {
    // Clear first so repeating the same text is announced again.
    this.message = '';
    clearTimeout(this.#timer);
    this.#timer = setTimeout(() => (this.message = text), 50);
  }
}

export const status = new Status();
