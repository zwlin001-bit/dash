import React, { useState, useEffect } from 'react';
import { NavLink, Outlet, useNavigate, Link, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ThemeToggle } from './ThemeToggle';
import {
  logout,
  useCurrentUser,
  fetchUnreadCount,
  fetchActiveAlerts,
  fetchJobs,
} from '../../api';
import { ErrorBoundary } from '../ErrorBoundary';
import styles from './Layout.module.css';

interface NavItem {
  key: string;
  label: string;
  path: string;
  icon: string;
  end?: boolean;
}

interface NavGroup {
  key: string;
  label: string;
  items: NavItem[];
}

const NAV_GROUPS: NavGroup[] = [
  {
    key: 'monitor',
    label: '监控',
    items: [
      { key: 'nodes', label: '节点总览', path: '/nodes', icon: '📊', end: true },
      { key: 'machines', label: '机器清单', path: '/machines', icon: '🖥️' },
      { key: 'events', label: '事件', path: '/events', icon: '🔔' },
      { key: 'alerts', label: '告警', path: '/alerts', icon: '🚨' },
    ],
  },
  {
    key: 'aliyun',
    label: '阿里云',
    items: [
      { key: 'cloud', label: '云资产', path: '/cloud', icon: '☁️' },
      { key: 'guard', label: '流量守卫', path: '/guard', icon: '🛡️' },
      { key: 'billing', label: '云账单', path: '/billing', icon: '💰' },
    ],
  },
  {
    key: 'system',
    label: '系统设置',
    items: [
      { key: 'jobs', label: '任务中心', path: '/jobs', icon: '⏱️' },
      { key: 'settings', label: '设置', path: '/settings', icon: '⚙️' },
    ],
  },
];

const COLLAPSED_STORAGE_KEY = 'dash_sidebar_collapsed_groups';

function loadCollapsedGroups(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(COLLAPSED_STORAGE_KEY);
    if (raw) {
      return JSON.parse(raw);
    }
  } catch {}
  return {};
}

function saveCollapsedGroups(state: Record<string, boolean>) {
  try {
    localStorage.setItem(COLLAPSED_STORAGE_KEY, JSON.stringify(state));
  } catch {}
}

export const Layout: React.FC = () => {
  const navigate = useNavigate();
  const location = useLocation();

  const { data: currentUser } = useCurrentUser();

  // 未读事件数 (用于顶栏事件铃与监控组/事件项徽章)
  const { data: unreadData } = useQuery({
    queryKey: ['events-unread-count'],
    queryFn: fetchUnreadCount,
    refetchInterval: 5000,
  });
  const unreadCount = unreadData?.unread_count || 0;

  // firing 状态活跃告警数 (用于监控组与告警项徽章)
  const { data: activeAlertsData } = useQuery({
    queryKey: ['active-alerts'],
    queryFn: fetchActiveAlerts,
    refetchInterval: 10000,
  });
  const firingAlertsCount = (activeAlertsData?.items || []).filter(
    (a) => a.event_state === 'firing'
  ).length;

  // 失败任务数 (用于系统设置组与任务中心徽章)
  const { data: failedJobsData } = useQuery({
    queryKey: ['jobs', 'failed', 'sidebar-badge'],
    queryFn: () => fetchJobs({ state: 'failed', limit: 1 }),
    refetchInterval: 15000,
  });
  const failedJobsCount = failedJobsData?.total || 0;

  // 找出当前路由所属的一级分组 key
  const currentPath = location.pathname;
  const activeGroupKey = NAV_GROUPS.find((group) =>
    group.items.some((item) =>
      item.end
        ? currentPath === item.path
        : currentPath === item.path || currentPath.startsWith(item.path + '/')
    )
  )?.key || 'monitor';

  // 折叠状态（存 localStorage）
  const [collapsedState, setCollapsedState] = useState<Record<string, boolean>>(loadCollapsedGroups);

  // 约束：当前路由所在的一级项必须展开，无论之前 localStorage 是否存过折叠
  useEffect(() => {
    if (activeGroupKey && collapsedState[activeGroupKey]) {
      setCollapsedState((prev) => {
        const next = { ...prev, [activeGroupKey]: false };
        saveCollapsedGroups(next);
        return next;
      });
    }
  }, [activeGroupKey]);

  const toggleGroup = (groupKey: string) => {
    setCollapsedState((prev) => {
      const isCurrentlyCollapsed = !!prev[groupKey];
      const next = { ...prev, [groupKey]: !isCurrentlyCollapsed };
      saveCollapsedGroups(next);
      return next;
    });
  };

  const handleLogout = async () => {
    await logout();
    navigate('/login', { replace: true });
  };

  // 各分组与子项的异常/未读徽章数
  const monitorBadgeCount = unreadCount + firingAlertsCount;
  const systemBadgeCount = failedJobsCount;

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
          <Link to="/events" className={styles.bellBtn} title="事件中心">
            <span className={styles.icon}>🔔</span>
            {unreadCount > 0 && <span className={styles.bellDot} />}
          </Link>
          <ThemeToggle />
          <span className={styles.who}>{currentUser?.username || 'admin'}</span>
          <button type="button" className="btn mini ghost" onClick={handleLogout} title="退出登录">
            退出
          </button>
        </div>
      </header>

      {/* 侧栏与主内容区 */}
      <div className={styles.bodyLayout}>
        <aside className={styles.sidebar}>
          <nav className={styles.menu}>
            {NAV_GROUPS.map((group) => {
              // 当前所在组强制展开
              const isCurrentGroup = group.key === activeGroupKey;
              const isCollapsed = isCurrentGroup ? false : !!collapsedState[group.key];

              // 计算分组总徽章（仅当存在未读/异常时才显示）
              let groupBadge = 0;
              if (group.key === 'monitor') {
                groupBadge = monitorBadgeCount;
              } else if (group.key === 'system') {
                groupBadge = systemBadgeCount;
              }

              return (
                <div key={group.key} className={styles.group}>
                  <button
                    type="button"
                    className={styles.groupHeader}
                    onClick={() => toggleGroup(group.key)}
                    aria-expanded={!isCollapsed}
                    title={`${group.label} (点击${isCollapsed ? '展开' : '折叠'})`}
                  >
                    <span
                      className={`${styles.groupChevron} ${
                        isCollapsed ? styles.groupChevronCollapsed : ''
                      }`}
                    >
                      ▼
                    </span>
                    <span className={styles.groupTitle}>{group.label}</span>
                    {groupBadge > 0 && (
                      <span className={styles.groupBadge} title="未读/异常项数">
                        {groupBadge > 99 ? '99+' : groupBadge}
                      </span>
                    )}
                  </button>

                  <div
                    className={`${styles.subList} ${
                      isCollapsed ? styles.subListCollapsed : ''
                    }`}
                  >
                    {group.items.map((item) => {
                      let itemBadge: React.ReactNode = null;
                      if (item.key === 'events' && unreadCount > 0) {
                        itemBadge = (
                          <span className={styles.itemBadge}>
                            {unreadCount > 99 ? '99+' : unreadCount}
                          </span>
                        );
                      } else if (item.key === 'alerts' && firingAlertsCount > 0) {
                        itemBadge = (
                          <span className={styles.itemBadgeErr}>
                            {firingAlertsCount > 99 ? '99+' : firingAlertsCount}
                          </span>
                        );
                      } else if (item.key === 'jobs' && failedJobsCount > 0) {
                        itemBadge = (
                          <span className={styles.itemBadgeErr}>
                            {failedJobsCount > 99 ? '99+' : failedJobsCount}
                          </span>
                        );
                      }

                      return (
                        <NavLink
                          key={item.key}
                          to={item.path}
                          end={item.end}
                          className={({ isActive }) =>
                            `${styles.subNavLink} ${isActive ? styles.subNavLinkActive : ''}`
                          }
                        >
                          <span className={styles.icon}>{item.icon}</span>
                          <span>{item.label}</span>
                          {itemBadge}
                        </NavLink>
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </nav>

          <div className={styles.sideFoot}>
            dash v0.1.0 · Single Binary
          </div>
        </aside>

        <main className={styles.mainContent}>
          <ErrorBoundary level="route" resetKey={location.pathname}>
            <Outlet />
          </ErrorBoundary>
        </main>
      </div>
    </div>
  );
};
