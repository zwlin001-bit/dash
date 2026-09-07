import React from 'react';
import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { ThemeToggle } from './ThemeToggle';
import { logout } from '../../api/auth';
import styles from './Layout.module.css';

export const Layout: React.FC = () => {
  const navigate = useNavigate();

  const handleLogout = async () => {
    await logout();
    navigate('/login');
  };

  return (
    <div className={styles.wrapper}>
      {/* 顶栏 (docs/13-ui-spec.md §4) */}
      <header className={styles.topbar}>
        <NavLink to="/nodes" className={styles.brand}>
          <span className={styles.brandName}>dash</span>
          <span className={styles.brandSub}>运维控制台</span>
        </NavLink>

        <div className="health ok" title="系统运行状态正常">
          <span className="hdot" />
          <span>system: ok</span>
        </div>

        <div className={styles.topRight}>
          <ThemeToggle />
          <span className={styles.who}>admin</span>
          <button type="button" className="btn mini ghost" onClick={handleLogout} title="退出登录">
            退出
          </button>
        </div>
      </header>

      {/* 侧栏与主内容区 */}
      <div className={styles.bodyLayout}>
        <aside className={styles.sidebar}>
          <nav className={styles.menu}>
            <NavLink
              to="/nodes"
              end
              className={({ isActive }) =>
                `${styles.navLink} ${isActive ? styles.navLinkActive : ''}`
              }
            >
              <span className={styles.icon}>📊</span>
              <span>节点总览</span>
            </NavLink>
            <NavLink
              to="/machines"
              className={({ isActive }) =>
                `${styles.navLink} ${isActive ? styles.navLinkActive : ''}`
              }
            >
              <span className={styles.icon}>🖥️</span>
              <span>机器清单</span>
            </NavLink>
            <NavLink
              to="/settings"
              className={({ isActive }) =>
                `${styles.navLink} ${isActive ? styles.navLinkActive : ''}`
              }
            >
              <span className={styles.icon}>⚙️</span>
              <span>系统设置</span>
            </NavLink>
          </nav>

          <div className={styles.sideFoot}>
            dash v0.1.0 · Single Binary
          </div>
        </aside>

        <main className={styles.mainContent}>
          <Outlet />
        </main>
      </div>
    </div>
  );
};
