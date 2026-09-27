import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import {
  LayoutDashboard,
  Globe,
  Server,
  Key,
  Puzzle,
  ScrollText,
  Settings,
  Users,
  LogOut,
} from 'lucide-react';

const navItems = [
  { to: '/', icon: LayoutDashboard, label: '仪表盘' },
  { to: '/endpoint', icon: Globe, label: '端点' },
  { to: '/providers', icon: Server, label: '提供商' },
  { to: '/accounts', icon: Key, label: '账户' },
  { to: '/combos', icon: Puzzle, label: '组合' },
  { to: '/logs', icon: ScrollText, label: '日志' },
  { to: '/settings', icon: Settings, label: '设置' },
  { to: '/users', icon: Users, label: '用户', admin: true },
];

const roleLabels: Record<string, string> = {
  admin: '管理员',
  user: '用户',
  member: '成员',
};

export default function Layout() {
  const navigate = useNavigate();
  const role = localStorage.getItem('role') || 'user';

  const handleLogout = () => {
    localStorage.removeItem('token');
    localStorage.removeItem('role');
    localStorage.removeItem('username');
    navigate('/login');
  };

  const filteredItems = navItems.filter(
    (item) => !item.admin || role === 'admin'
  );

  return (
    <div className="flex h-screen bg-gray-50">
      <aside className="w-64 bg-gray-900 text-white flex flex-col shrink-0">
        <div className="p-6 border-b border-gray-800">
          <h1 className="text-xl font-bold tracking-tight">AI 路由网关</h1>
          <p className="text-gray-400 text-sm mt-1">管理面板</p>
        </div>
        <nav className="flex-1 px-3 py-4 space-y-1 overflow-y-auto">
          {filteredItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/'}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-indigo-600 text-white'
                    : 'text-gray-300 hover:bg-gray-800 hover:text-white'
                }`
              }
            >
              <item.icon className="w-5 h-5" />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="p-3 border-t border-gray-800">
          <button
            onClick={handleLogout}
            className="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium text-gray-300 hover:bg-gray-800 hover:text-white w-full transition-colors"
          >
            <LogOut className="w-5 h-5" />
            退出登录
          </button>
        </div>
      </aside>
      <div className="flex-1 flex flex-col min-w-0">
        <header className="h-16 bg-white border-b border-gray-200 flex items-center justify-between px-6 shrink-0">
          <h2 className="text-lg font-semibold text-gray-900">AI 路由网关</h2>
          <div className="flex items-center gap-3">
            <span className="text-sm text-gray-600 font-medium">
              当前用户：<strong className="text-gray-800">{localStorage.getItem('username') || '用户'}</strong>
            </span>
            <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-indigo-100 text-indigo-800">
              {roleLabels[role] || role}
            </span>
          </div>
        </header>
        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
