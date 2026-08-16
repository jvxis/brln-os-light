const JOURNAL_ISO_PREFIX = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(\.\d+)?(Z|[+-]\d{2}:?\d{2})\s+(.*)$/

export function formatJournalLogLine(line: string, locale: string): string {
  const match = String(line || '').match(JOURNAL_ISO_PREFIX)
  if (!match) return line

  const [, base, fraction = '', rawZone, message] = match
  const milliseconds = fraction ? fraction.slice(0, 4) : ''
  const zone = /^[+-]\d{4}$/.test(rawZone)
    ? `${rawZone.slice(0, 3)}:${rawZone.slice(3)}`
    : rawZone
  const parsed = new Date(`${base}${milliseconds}${zone}`)
  if (Number.isNaN(parsed.getTime())) return line

  const timestamp = parsed.toLocaleString(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
  return `${timestamp} ${message}`
}

export function formatJournalLogLines(lines: string[], locale: string): string[] {
  return lines.map((line) => formatJournalLogLine(line, locale))
}
