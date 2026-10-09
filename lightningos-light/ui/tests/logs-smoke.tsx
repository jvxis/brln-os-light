import React from 'react'
import { createRoot } from 'react-dom/client'
import '../src/styles/main.css'
import '../src/i18n'
import Logs from '../src/pages/Logs'

createRoot(document.getElementById('root')!).render(<main className="mx-auto max-w-7xl p-4 text-fog"><Logs /></main>)
