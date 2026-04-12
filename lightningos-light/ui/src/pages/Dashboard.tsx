import type { AuthState } from '../api'
import DashboardScreen from '../components/dashboard/DashboardScreen'

type DashboardProps = {
  authState?: AuthState | null
}

export default function Dashboard(props: DashboardProps) {
  return <DashboardScreen {...props} />
}
