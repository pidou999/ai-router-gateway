import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import ProtectedRoute from './components/ProtectedRoute';
import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import Providers from './pages/Providers';
import ProviderDetail from './pages/ProviderDetail';
import Accounts from './pages/Accounts';
import Combos from './pages/Combos';
import Logs from './pages/Logs';
import Settings from './pages/Settings';
import Users from './pages/Users';
import Endpoint from './pages/Endpoint';

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          element={
            <ProtectedRoute>
              <Layout />
            </ProtectedRoute>
          }
        >
          <Route index element={<Dashboard />} />
          <Route path="endpoint" element={<Endpoint />} />
          <Route path="providers" element={<Providers />} />
          <Route path="providers/:id" element={<ProviderDetail />} />
          <Route path="accounts" element={<Accounts />} />
          <Route path="combos" element={<Combos />} />
          <Route path="logs" element={<Logs />} />
          <Route path="settings" element={<Settings />} />
          <Route path="users" element={<Users />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
