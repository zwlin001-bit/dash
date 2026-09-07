import React, { useState } from 'react';
import { useNavigate, useLocation, useSearchParams } from 'react-router-dom';
import { login } from '../../api/auth';
import styles from './Login.module.css';

export const Login: React.FC = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams] = useSearchParams();

  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [loading, setLoading] = useState(false);

  const getRedirectTarget = (): string => {
    const fromQuery = searchParams.get('from');
    if (
      fromQuery &&
      fromQuery.startsWith('/') &&
      !fromQuery.startsWith('//') &&
      !fromQuery.startsWith('/login')
    ) {
      return fromQuery;
    }

    const fromState = (location.state as any)?.from;
    if (fromState) {
      if (
        typeof fromState === 'string' &&
        fromState.startsWith('/') &&
        !fromState.startsWith('//') &&
        !fromState.startsWith('/login')
      ) {
        return fromState;
      }
      if (
        fromState.pathname &&
        typeof fromState.pathname === 'string' &&
        fromState.pathname.startsWith('/') &&
        !fromState.pathname.startsWith('/login')
      ) {
        return fromState.pathname + (fromState.search || '') + (fromState.hash || '');
      }
    }

    return '/nodes';
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!username || !password) {
      setErrorMsg('请输入用户名和密码');
      return;
    }

    setLoading(true);
    setErrorMsg('');
    try {
      await login({ username, password });
      const target = getRedirectTarget();
      navigate(target, { replace: true });
    } catch (err: any) {
      if (err?.status === 429 || err?.code === 'rate_limited') {
        setErrorMsg('尝试过多，请 15 分钟后再试');
      } else if (err?.code === 'bad_credentials') {
        setErrorMsg(err?.message || '用户名或密码错误');
      } else {
        setErrorMsg(err?.message || '登录失败，请检查账号密码');
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className={styles.container}>
      <div className={styles.loginCard}>
        <div className={styles.header}>
          <div className={styles.title}>DASH</div>
          <div className={styles.sub}>个人 VPS 运维控制台</div>
        </div>

        {errorMsg && <div className={styles.errorBanner}>{errorMsg}</div>}

        <form onSubmit={handleSubmit} className={styles.form}>
          <div className={styles.formGroup}>
            <label className={styles.label}>用户名</label>
            <input
              type="text"
              className={styles.input}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="admin"
              autoFocus
              disabled={loading}
            />
          </div>

          <div className={styles.formGroup}>
            <label className={styles.label}>密码</label>
            <input
              type="password"
              className={styles.input}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="请输入密码"
              disabled={loading}
            />
          </div>

          <button
            type="submit"
            className="btn primary"
            disabled={loading}
            style={{ width: '100%', padding: '9px 0', marginTop: '8px' }}
          >
            {loading ? '登录中...' : '登 录'}
          </button>
        </form>

        <div className={styles.footer}>
          dash · Single Binary
        </div>
      </div>
    </div>
  );
};
