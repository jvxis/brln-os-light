import React, { useState } from 'react'
import { createRoot } from 'react-dom/client'
import '../src/styles/main.css'
import '../src/i18n'
import DashboardScreen from '../src/components/dashboard/DashboardScreen'

function Fixture() {
  const [mounted, setMounted] = useState(true)
  return <main className="mx-auto max-w-7xl p-4 text-fog">
    {mounted && <DashboardScreen />}
    <button onClick={() => setMounted(false)}>Unmount dashboard</button>
  </main>
}

createRoot(document.getElementById('root')!).render(<Fixture />)
