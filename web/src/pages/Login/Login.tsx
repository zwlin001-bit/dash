import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { login } from '../../api/auth';
import styles from './Login.module.css';

export const Login: React.FC = () => {
  const navigate = useNavigate();
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [loading, setLoading] = useState(false);

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
      navigate('/nodes');
    } catch (err: any) {
      setErrorMsg(err?.message || '登录失败，请检查账号密码');
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

          <button type="submit" className="btn primary" disabled={loading} style={{ width: '100%', padding: '9px 0', marginTop: '8px' }}>
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
