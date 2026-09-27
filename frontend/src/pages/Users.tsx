import { useState, useEffect } from 'react';
import api from '../api/client';
import type { User } from '../types';

const roleLabel: Record<string, string> = {
  admin: '管理员',
  member: '普通用户',
};

export default function Users() {
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // 编辑弹窗状态
  const [editing, setEditing] = useState<User | null>(null);
  const [currentPwd, setCurrentPwd] = useState('');
  const [newPwd, setNewPwd] = useState('');
  const [confirmPwd, setConfirmPwd] = useState('');
  const [role, setRole] = useState<'admin' | 'member'>('member');
  const [saving, setSaving] = useState(false);
  const [editError, setEditError] = useState('');
  const [editMsg, setEditMsg] = useState('');

  const fetchUsers = async () => {
    try {
      const { data } = await api.get<User[]>('/admin/users');
      setUsers(data);
      setError('');
    } catch {
      setError('加载用户失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchUsers();
  }, []);

  const openEdit = (u: User) => {
    setEditing(u);
    setCurrentPwd('');
    setNewPwd('');
    setConfirmPwd('');
    setRole(u.role === 'admin' ? 'admin' : 'member');
    setEditError('');
    setEditMsg('');
  };

  const closeEdit = () => {
    setEditing(null);
    setCurrentPwd('');
    setNewPwd('');
    setConfirmPwd('');
    setEditError('');
    setEditMsg('');
  };

  const saveEdit = async () => {
    if (!editing) return;
    setEditError('');
    setEditMsg('');

    // 如果填了新密码，必须验证当前密码
    if (newPwd || confirmPwd) {
      if (!currentPwd) {
        setEditError('修改密码需要填写当前密码');
        return;
      }
      if (newPwd.length < 6) {
        setEditError('新密码至少 6 位');
        return;
      }
      if (newPwd !== confirmPwd) {
        setEditError('两次输入的新密码不一致');
        return;
      }
    }

    setSaving(true);
    try {
      const payload: Record<string, unknown> = { role };
      // 只有同时提供当前密码和新密码时才发送密码字段（与 ChangePassword 接口一致）
      if (currentPwd && newPwd) {
        payload.currentPassword = currentPwd;
        payload.newPassword = newPwd;
      }
      await api.put(`/admin/users/${editing.id}`, payload);
      setEditMsg('保存成功');
      closeEdit();
      await fetchUsers();
      setError('');
    } catch (err: any) {
      setEditError(err?.response?.data?.error || '更新用户失败');
    } finally {
      setSaving(false);
    }
  };

  const deleteUser = async (u: User) => {
    if (!window.confirm(`确定要删除用户「${u.username}」吗？此操作不可恢复。`)) {
      return;
    }
    try {
      await api.delete(`/admin/users/${u.id}`);
      await fetchUsers();
      setError('');
    } catch (err: any) {
      setError(err?.response?.data?.error || '删除用户失败');
    }
  };

  if (loading) {
    return (
      <div className="space-y-6">
        <h2 className="text-2xl font-bold text-gray-800">用户</h2>
        <div className="bg-white rounded-xl shadow-sm p-6 text-gray-400">加载中…</div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold text-gray-800">用户</h2>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 px-4 py-3 rounded-lg">
          {error}
        </div>
      )}

      <div className="bg-white rounded-xl shadow-sm overflow-hidden">
        <table className="w-full">
          <thead>
            <tr className="bg-gray-50 text-left text-sm text-gray-500">
              <th className="px-6 py-3 font-medium">用户名</th>
              <th className="px-6 py-3 font-medium">邮箱</th>
              <th className="px-6 py-3 font-medium">角色</th>
              <th className="px-6 py-3 font-medium">创建时间</th>
              <th className="px-6 py-3 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {users.map((u) => (
              <tr key={u.id} className="hover:bg-gray-50">
                <td className="px-6 py-4 font-medium text-gray-800">{u.username}</td>
                <td className="px-6 py-4 text-gray-600">{u.email}</td>
                <td className="px-6 py-4">
                  <span
                    className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
                      u.role === 'admin'
                        ? 'bg-purple-100 text-purple-700'
                        : 'bg-gray-100 text-gray-700'
                    }`}
                  >
                    {roleLabel[u.role] ?? u.role}
                  </span>
                </td>
                <td className="px-6 py-4 text-gray-600">
                  {new Date(u.created_at).toLocaleDateString()}
                </td>
                <td className="px-6 py-4 text-right whitespace-nowrap">
                  <button
                    onClick={() => openEdit(u)}
                    className="text-sm text-blue-600 hover:text-blue-800 font-medium mr-4"
                  >
                    编辑
                  </button>
                  <button
                    onClick={() => deleteUser(u)}
                    className="text-sm text-red-600 hover:text-red-800 font-medium"
                  >
                    删除
                  </button>
                </td>
              </tr>
            ))}
            {users.length === 0 && (
              <tr>
                <td colSpan={5} className="px-6 py-8 text-center text-gray-400">
                  暂无用户
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {editing && (
        <div
          className="fixed inset-0 bg-black/40 flex items-center justify-center z-50"
          onClick={closeEdit}
        >
          <div
            className="bg-white rounded-xl p-6 w-full max-w-md shadow-xl"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-lg font-bold text-gray-800 mb-4">
              编辑用户 · {editing.username}
            </h3>

            {editError && (
              <div className="mb-3 bg-red-50 border border-red-200 text-red-700 px-3 py-2 rounded-lg text-sm">
                {editError}
              </div>
            )}
            {editMsg && (
              <div className="mb-3 bg-green-50 border border-green-200 text-green-700 px-3 py-2 rounded-lg text-sm">
                {editMsg}
              </div>
            )}

            <details className="mb-4">
              <summary className="cursor-pointer text-sm font-medium text-gray-600 hover:text-gray-800 select-none">
                修改密码（可选）
              </summary>
              <div className="mt-3 space-y-3 pl-1">
                <div>
                  <label className="block text-sm text-gray-600 mb-1">当前密码</label>
                  <input
                    type="password"
                    value={currentPwd}
                    onChange={(e) => setCurrentPwd(e.target.value)}
                    placeholder="修改密码时必填"
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                  />
                </div>
                <div>
                  <label className="block text-sm text-gray-600 mb-1">新密码</label>
                  <input
                    type="password"
                    value={newPwd}
                    onChange={(e) => setNewPwd(e.target.value)}
                    placeholder="至少 6 位"
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                  />
                </div>
                <div>
                  <label className="block text-sm text-gray-600 mb-1">确认新密码</label>
                  <input
                    type="password"
                    value={confirmPwd}
                    onChange={(e) => setConfirmPwd(e.target.value)}
                    placeholder="再次输入新密码"
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                  />
                </div>
                <p className="text-xs text-gray-400">留空则不修改密码；修改密码必须填写当前密码</p>
              </div>
            </details>

            <label className="block text-sm font-medium text-gray-700 mb-1">
              角色
            </label>
            <select
              value={role}
              onChange={(e) => setRole(e.target.value as 'admin' | 'member')}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 mb-6"
            >
              <option value="member">普通用户</option>
              <option value="admin">管理员</option>
            </select>

            <div className="flex justify-end gap-2">
              <button
                onClick={closeEdit}
                className="px-4 py-2 text-sm text-gray-600 hover:text-gray-800"
              >
                取消
              </button>
              <button
                onClick={saveEdit}
                disabled={saving}
                className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50"
              >
                {saving ? '保存中…' : '保存'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
