export type ThemeMode = 'dark' | 'light'

export type VisualThemeKey =
  | 'sovereign-grid'
  | 'signal-deck'
  | 'block-zero'
  | 'airplane-mode'
  | 'miner-watch'
  | 'kernel'

export type PaletteKey =
  | 'teal'
  | 'bitcoin'
  | 'ocean'
  | 'sunset'
  | 'orchid'
  | 'forest'
  | 'aurora'
  | 'ember'
  | 'slate'
  | 'phosphor-white'
  | 'phosphor-amber'
  | 'phosphor-green'

export type Appearance = {
  visualTheme: VisualThemeKey
  palette: PaletteKey
  mode: ThemeMode
}

export const visualThemeOrder: VisualThemeKey[] = [
  'sovereign-grid',
  'signal-deck',
  'block-zero',
  'airplane-mode',
  'miner-watch',
  'kernel'
]

export const paletteOrder: PaletteKey[] = [
  'teal',
  'bitcoin',
  'ocean',
  'sunset',
  'orchid',
  'forest',
  'aurora',
  'ember',
  'slate',
  'phosphor-white',
  'phosphor-amber',
  'phosphor-green'
]

export const darkOnlyThemes: VisualThemeKey[] = ['airplane-mode', 'miner-watch', 'kernel']
export const legacyTerminalPalettes: PaletteKey[] = [
  'phosphor-white',
  'phosphor-amber',
  'phosphor-green'
]

export const defaultVisualTheme: VisualThemeKey = 'sovereign-grid'
export const legacyVisualTheme: VisualThemeKey = 'signal-deck'
export const defaultPalette: PaletteKey = 'teal'

export const appearanceStorageKeys = {
  visualTheme: 'los-visual-theme',
  palette: 'los-palette',
  mode: 'los-theme'
} as const

export const resolveTheme = (value: string | null): ThemeMode => (value === 'light' ? 'light' : 'dark')

export const isDarkOnlyTheme = (value: VisualThemeKey): boolean => darkOnlyThemes.includes(value)

export const resolvePalette = (value: string | null): PaletteKey => {
  if (value && paletteOrder.includes(value as PaletteKey)) {
    return value as PaletteKey
  }
  return defaultPalette
}

export const resolveVisualTheme = (value: string | null): VisualThemeKey => {
  if (value && visualThemeOrder.includes(value as VisualThemeKey)) {
    return value as VisualThemeKey
  }
  return defaultVisualTheme
}

export const resolveStoredAppearance = (storage: Pick<Storage, 'getItem'>): Appearance => {
  const storedVisualTheme = storage.getItem(appearanceStorageKeys.visualTheme)
  const storedPalette = storage.getItem(appearanceStorageKeys.palette)
  const storedMode = storage.getItem(appearanceStorageKeys.mode)
  const palette = resolvePalette(storedPalette)

  // Before 0.5.0, the visual treatment was encoded in the palette. Preserve
  // that selection while moving it to the independent theme dimension.
  let visualTheme: VisualThemeKey
  if (storedVisualTheme) {
    visualTheme = resolveVisualTheme(storedVisualTheme)
  } else if (storedPalette && legacyTerminalPalettes.includes(palette)) {
    visualTheme = 'miner-watch'
  } else if (storedPalette || storedMode) {
    visualTheme = legacyVisualTheme
  } else {
    visualTheme = defaultVisualTheme
  }

  const requestedMode = resolveTheme(storedMode)
  const mode = isDarkOnlyTheme(visualTheme) ? 'dark' : requestedMode
  return { visualTheme, palette, mode }
}

export const applyAppearance = ({ visualTheme, palette, mode }: Appearance): void => {
  const root = document.documentElement
  root.setAttribute('data-visual-theme', visualTheme)
  root.setAttribute('data-palette', palette)
  root.setAttribute('data-theme', isDarkOnlyTheme(visualTheme) ? 'dark' : mode)
}
