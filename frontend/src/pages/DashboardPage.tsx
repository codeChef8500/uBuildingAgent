import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'

const STATS = [
  { label: '总巡检次数', value: 38, color: 'bg-indigo-500', icon: '🔍' },
  { label: '发现风险', value: 12, color: 'bg-red-500', icon: '⚠️' },
  { label: '待处理工单', value: 7, color: 'bg-amber-500', icon: '📋' },
  { label: '已完成整改', value: 19, color: 'bg-green-500', icon: '✅' },
]

const TREND_DATA = [
  { day: '5/22', inspections: 4, risks: 2 },
  { day: '5/23', inspections: 6, risks: 3 },
  { day: '5/24', inspections: 5, risks: 1 },
  { day: '5/25', inspections: 8, risks: 4 },
  { day: '5/26', inspections: 5, risks: 2 },
  { day: '5/27', inspections: 7, risks: 3 },
  { day: '5/28', inspections: 3, risks: 1 },
]

const RISK_COLORS: Record<string, string> = {
  critical: 'bg-red-500 text-white',
  high: 'bg-orange-500 text-white',
  medium: 'bg-amber-400 text-black',
  low: 'bg-green-500 text-white',
}
const RISK_LABELS: Record<string, string> = {
  critical: '严重',
  high: '高',
  medium: '中',
  low: '低',
}

const STATUS_COLORS: Record<string, string> = {
  completed: 'bg-green-100 text-green-700',
  pending: 'bg-amber-100 text-amber-700',
  processing: 'bg-blue-100 text-blue-700',
}
const STATUS_LABELS: Record<string, string> = {
  completed: '已完成',
  pending: '待处理',
  processing: '处理中',
}

const RECENT_RECORDS = [
  { time: '2026-05-28 08:42', location: 'A栋3楼东侧脚手架', risk: 'critical', status: 'processing' },
  { time: '2026-05-27 15:10', location: 'B栋基坑开挖区域', risk: 'high', status: 'completed' },
  { time: '2026-05-27 09:30', location: 'C栋屋面防水施工', risk: 'medium', status: 'completed' },
  { time: '2026-05-26 16:55', location: 'D栋地下室混凝土浇筑', risk: 'low', status: 'completed' },
  { time: '2026-05-26 10:20', location: 'A栋外墙涂料施工', risk: 'medium', status: 'pending' },
  { time: '2026-05-25 14:00', location: 'E栋钢结构吊装', risk: 'high', status: 'pending' },
]

export function DashboardPage() {
  return (
    <div className="p-6 space-y-6 max-w-6xl">
      <div>
        <h1 className="text-2xl font-bold text-gray-900">主控台</h1>
        <p className="text-gray-500 text-sm mt-1">施工现场安全巡检概览</p>
      </div>

      {/* Stats cards */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {STATS.map((s) => (
          <div key={s.label} className="bg-white rounded-xl p-4 border border-gray-200 shadow-sm">
            <div className="flex items-center justify-between mb-3">
              <span className="text-gray-500 text-xs">{s.label}</span>
              <span className="text-xl">{s.icon}</span>
            </div>
            <div className="flex items-end gap-2">
              <span className="text-3xl font-bold text-gray-900">{s.value}</span>
              <span className="text-gray-400 text-xs mb-1">件</span>
            </div>
            <div className={`h-1 rounded-full mt-3 ${s.color} opacity-60`} />
          </div>
        ))}
      </div>

      {/* Trend chart */}
      <div className="bg-white rounded-xl p-5 border border-gray-200 shadow-sm">
        <h2 className="text-sm font-semibold text-gray-700 mb-4">近7日巡检趋势</h2>
        <ResponsiveContainer width="100%" height={160}>
          <LineChart data={TREND_DATA}>
            <XAxis dataKey="day" tick={{ fill: '#6b7280', fontSize: 11 }} axisLine={false} tickLine={false} />
            <YAxis tick={{ fill: '#6b7280', fontSize: 11 }} axisLine={false} tickLine={false} width={24} />
            <Tooltip
              contentStyle={{ background: '#ffffff', border: '1px solid #e5e7eb', borderRadius: 8 }}
              labelStyle={{ color: '#111827' }}
              itemStyle={{ color: '#6b7280' }}
            />
            <Line type="monotone" dataKey="inspections" stroke="#6366f1" strokeWidth={2} dot={{ r: 3 }} name="巡检" />
            <Line type="monotone" dataKey="risks" stroke="#ef4444" strokeWidth={2} dot={{ r: 3 }} name="风险" />
          </LineChart>
        </ResponsiveContainer>
      </div>

      {/* Recent records table */}
      <div className="bg-white rounded-xl border border-gray-200 shadow-sm overflow-hidden">
        <div className="px-5 py-4 border-b border-gray-200">
          <h2 className="text-sm font-semibold text-gray-700">最近巡检记录</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-gray-500 text-xs bg-gray-50">
                <th className="text-left px-5 py-3 font-medium">时间</th>
                <th className="text-left px-5 py-3 font-medium">巡检地点</th>
                <th className="text-left px-5 py-3 font-medium">风险等级</th>
                <th className="text-left px-5 py-3 font-medium">状态</th>
              </tr>
            </thead>
            <tbody>
              {RECENT_RECORDS.map((r, i) => (
                <tr key={i} className="border-t border-gray-100 hover:bg-gray-50 transition-colors">
                  <td className="px-5 py-3 text-gray-400 text-xs">{r.time}</td>
                  <td className="px-5 py-3 text-gray-800">{r.location}</td>
                  <td className="px-5 py-3">
                    <span className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${RISK_COLORS[r.risk] ?? ''}`}>
                      {RISK_LABELS[r.risk] ?? r.risk}
                    </span>
                  </td>
                  <td className="px-5 py-3">
                    <span className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${STATUS_COLORS[r.status] ?? ''}`}>
                      {STATUS_LABELS[r.status] ?? r.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
