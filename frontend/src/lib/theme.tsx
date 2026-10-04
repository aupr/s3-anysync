import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

export type Theme = 'system' | 'light' | 'dark'
const KEY = 's3as.theme'

export function loadTheme(): Theme {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // storage unavailable
  }
  return 'system'
}

function saveTheme(t: Theme) {
  try {
    localStorage.setItem(KEY, t)
  } catch {
    // storage unavailable
  }
}

export const isDark = (t: Theme, prefersDark: boolean) => t === 'dark' || (t === 'system' && prefersDark)

type ThemeValue = [Theme, (t: Theme) => void]
const Ctx = createContext<ThemeValue | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>(loadTheme)
  useEffect(() => {
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)')
    const apply = () => document.documentElement.classList.toggle('dark', isDark(theme, !!mq?.matches))
    apply()
    saveTheme(theme)
    mq?.addEventListener?.('change', apply)
    return () => mq?.removeEventListener?.('change', apply)
  }, [theme])
  const value = useMemo<ThemeValue>(() => [theme, setTheme], [theme])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useTheme(): ThemeValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useTheme outside ThemeProvider')
  return v
}
