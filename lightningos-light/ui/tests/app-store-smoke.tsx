import React from 'react'
import { createRoot } from 'react-dom/client'
import '../src/styles/main.css'
import '../src/i18n'
import AppStore from '../src/pages/AppStore'
import { getAuthState } from '../src/api'

getAuthState().then(() => createRoot(document.getElementById('root')!).render(<main className="p-4"><AppStore /></main>))
