import React, { useEffect } from 'react';
import {
  BrowserRouter,
  Routes,
  Route,
  Navigate,
  useLocation,
  useNavigate,
} from 'react-router-dom';
import { QueryClientProvider, useQuery } from '@tanstack/react-query';
import { Layout } from './components/Layout';
import { Login, Overview, NodeDetail, Machines, Settings, Events, Cloud, Jobs, CloudGuard } from './pages';
import { queryClient, useCurrentUser, setUnauthorizedNavigator, checkHealth } from './api';
import { ErrorBoundary } from './components/ErrorBoundary';

export const ProtectedRoute: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { data: user, isLoading, isError } = useCurrentUser();
  const location = useLocation();

  if (isLoading) {
    return (
      <div className="dash-app-loading">
        <div className="dash-app-loading-spinner" />
        <span>正在验证登录状态...</span>
      </div>
    );
  }

  if (isError || !user) {
    const fromPath = location.pathname + location.search + location.hash;
    return <Navigate to={`/login?from=${encodeURIComponent(fromPath)}`} replace />;
  }

  return <>{children}</>;
};

const AuthNavigatorSync: React.FC = () => {
  const navigate = useNavigate();

  useEffect(() => {
    setUnauthorizedNavigator((target: string) => {
      navigate(target, { replace: true });
    });
    return () => {
      setUnauthorizedNavigator(null);
    };
  }, [navigate]);

  return null;
};

export const DevNoAuthBanner: React.FC = () => {
  const { data: health } = useQuery({
    queryKey: ['healthz'],
    queryFn: checkHealth,
    staleTime: 30000,
  });

  if (!health?.dev_no_auth) {
    return null;
  }

  return (
    <div className="dev-no-auth-banner" role="alert">
      ⚠️ 开发模式：鉴权已关闭
    </div>
  );
};

export const AppRoutes: React.FC = () => {
  return (
    <>
      <AuthNavigatorSync />
      <DevNoAuthBanner />
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          path="/"
          element={
            <ProtectedRoute>
              <Layout />
            </ProtectedRoute>
          }
        >
          <Route index element={<Navigate to="/nodes" replace />} />
          <Route path="nodes" element={<Overview />} />
          <Route path="nodes/:id" element={<NodeDetail />} />
          <Route path="machines" element={<Machines />} />
          <Route path="cloud" element={<Cloud />} />
          <Route path="guard" element={<CloudGuard />} />
          <Route path="jobs" element={<Jobs />} />
          <Route path="events" element={<Events />} />
          <Route path="settings" element={<Settings />} />
        </Route>
        <Route path="*" element={<Navigate to="/nodes" replace />} />
      </Routes>
    </>
  );
};

export const App: React.FC = () => {
  return (
    <ErrorBoundary level="app">
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <AppRoutes />
        </BrowserRouter>
      </QueryClientProvider>
    </ErrorBoundary>
  );
};
