// Copying from the LightningOS UI.
//
// The UI is reached on the local network over HTTPS with a self-signed certificate, a page the
// browser marks "Not secure". There navigator.clipboard may be missing or refuse to write, and every
// copy button failed: an operator could not copy the BR⚡LN Community signer password into the
// signer page (16/10/2026). This tries the Clipboard API, then the older copy command, which still
// works on such pages, and as a last resort shows the text selected in a prompt to copy by hand.
// It never throws, so a button's success path runs once the text is in reach.

const MANUAL_COPY_LABEL = 'Copie com Ctrl+C / Copy with Ctrl+C'

const copyWithCommand = (value: string): boolean => {
  const area = document.createElement('textarea')
  area.value = value
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '0'
  area.style.left = '0'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.focus()
  area.select()
  let copied = false
  try {
    copied = document.execCommand('copy')
  } catch {
    copied = false
  }
  document.body.removeChild(area)
  return copied
}

/** Copies `value`; resolves true when it reached the clipboard, false when it was shown for manual copy. */
export const copyText = async (value: string): Promise<boolean> => {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      // Denied or unavailable: the older command below still works on most pages.
    }
  }
  if (copyWithCommand(value)) {
    return true
  }
  window.prompt(MANUAL_COPY_LABEL, value)
  return false
}
