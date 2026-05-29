import { useState } from 'react'
import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'

const IconChart = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="1" y="9" width="3" height="6" rx="1" fill="currentColor" opacity="0.6"/>
    <rect x="6" y="5" width="3" height="10" rx="1" fill="currentColor" opacity="0.8"/>
    <rect x="11" y="1" width="3" height="14" rx="1" fill="currentColor"/>
  </svg>
)

const IconFile = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M3 2a1 1 0 011-1h5.586a1 1 0 01.707.293l3.414 3.414A1 1 0 0114 5.414V14a1 1 0 01-1 1H4a1 1 0 01-1-1V2z" stroke="currentColor" strokeWidth="1.2" fill="none"/>
    <path d="M9 1v4h4" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round"/>
    <path d="M5 9h6M5 12h4" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
  </svg>
)

const IconShield = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M8 1.5L2.5 4v4c0 3.5 2.5 5.8 5.5 6.5 3-0.7 5.5-3 5.5-6.5V4L8 1.5z" stroke="currentColor" strokeWidth="1.2" fill="none" strokeLinejoin="round"/>
    <path d="M5.5 8l1.8 1.8L10.5 6" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/>
  </svg>
)

const IconArchive = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect x="1" y="1.5" width="14" height="3.5" rx="1" stroke="currentColor" strokeWidth="1.2"/>
    <path d="M2.5 5v8a1 1 0 001 1h9a1 1 0 001-1V5" stroke="currentColor" strokeWidth="1.2"/>
    <path d="M6 9h4" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
  </svg>
)

const LogoShield = () => (
  <svg width="22" height="22" viewBox="0 0 22 22" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M11 2L3 6v5c0 4.5 3.3 7.8 8 9 4.7-1.2 8-4.5 8-9V6L11 2z" fill="#4F46E5" fillOpacity="0.15" stroke="#4F46E5" strokeWidth="1.5" strokeLinejoin="round"/>
    <path d="M7.5 11l2.5 2.5L14.5 8" stroke="#4F46E5" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"/>
  </svg>
)

interface NavItem {
  icon: ReactNode
  label: string
  to: string
}

const NAV_ITEMS: NavItem[] = [
  { icon: <IconChart />, label: '数据概览', to: '/' },
  { icon: <IconShield />, label: 'AI 哨兵', to: '/safeagent' },
  { icon: <IconFile />, label: '历史记录', to: '/history' },
  { icon: <IconArchive />, label: '数据分析', to: '/analytics' },
]

export function Sidebar() {
  const [collapsed, setCollapsed] = useState(false)

  return (
    <aside className={`relative flex flex-col bg-white border-r border-gray-100 shrink-0 h-screen sticky top-0 transition-all duration-200 ${collapsed ? 'w-16' : 'w-48'}`}>
      {/* Logo */}
      <div className="flex items-center gap-2.5 px-4 py-5">
        <LogoShield />
        {!collapsed && (
          <span className="text-gray-900 font-semibold text-sm whitespace-nowrap">AI 安全哨兵</span>
        )}
      </div>

      {/* Navigation */}
      <nav className="flex-1 px-3 space-y-0.5">
        {NAV_ITEMS.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.to === '/'}
            className={({ isActive }) =>
              `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors ${
                isActive
                  ? 'bg-indigo-600 text-white shadow-sm'
                  : 'text-gray-500 hover:bg-gray-50 hover:text-gray-800'
              }`
            }
          >
            <span className="shrink-0">{item.icon}</span>
            {!collapsed && <span className="truncate">{item.label}</span>}
          </NavLink>
        ))}
      </nav>

      {/* Collapse toggle */}
      <button
        onClick={() => setCollapsed((v) => !v)}
        className="absolute -right-3 top-6 w-6 h-6 rounded-full bg-white border border-gray-200 flex items-center justify-center text-gray-400 hover:text-gray-600 hover:border-gray-300 shadow-sm transition-colors z-10"
      >
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
          <path d={collapsed ? 'M3 2l4 3-4 3' : 'M7 2L3 5l4 3'} stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      </button>

      {/* Footer */}
      <div className={`px-3 py-4 border-t border-gray-100 ${collapsed ? 'flex justify-center' : ''}`}>
        <div className="flex items-center gap-2.5">
          <div className="w-7 h-7 rounded-full bg-indigo-100 flex items-center justify-center text-xs text-indigo-600 font-medium shrink-0">
            管
          </div>
          {!collapsed && <span className="text-xs text-gray-500 truncate">管理员</span>}
        </div>
      </div>
    </aside>
  )
}
