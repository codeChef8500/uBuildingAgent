import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { Sidebar } from './components/Sidebar'
import { DashboardPage } from './pages/DashboardPage'
import { SafeAgentPage } from './pages/SafeAgentPage'
import { PlaceholderPage } from './pages/PlaceholderPage'

function App() {
  return (
    <BrowserRouter>
      <div className="flex h-screen bg-gray-50 text-gray-900 overflow-hidden">
        <Sidebar />
        <main className="flex-1 overflow-auto">
          <Routes>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/safeagent" element={<SafeAgentPage />} />
            <Route path="/analytics" element={<PlaceholderPage title="数据分析" icon="📊" />} />
            <Route path="/history" element={<PlaceholderPage title="历史记录" icon="📋" />} />
            <Route path="/settings" element={<PlaceholderPage title="系统设置" icon="⚙️" />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}

export default App
