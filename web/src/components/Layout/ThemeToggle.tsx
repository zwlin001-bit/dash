import React, { useState, useEffect } from 'react';

export const ThemeToggle: React.FC = () => {
  const [theme, setTheme] = useState<'light' | 'dark' | 'auto'>('auto');

  useEffect(() => {
    const saved = localStorage.getItem('dash_theme') as 'light' | 'dark' | null;
    if (saved) {
      setTheme(saved);
      document.documentElement.setAttribute('data-theme', saved);
    } else {
      setTheme('auto');
    }
  }, []);

  const toggleTheme = () => {
    let next: 'light' | 'dark' | 'auto';
    if (theme === 'auto') {
      next = 'dark';
    } else if (theme === 'dark') {
      next = 'light';
    } else {
      next = 'auto';
    }

    setTheme(next);
    if (next === 'auto') {
      localStorage.removeItem('dash_theme');
      const isDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
      document.documentElement.setAttribute('data-theme', isDark ? 'dark' : 'light');
    } else {
      localStorage.setItem('dash_theme', next);
      document.documentElement.setAttribute('data-theme', next);
    }
  };

  const getIcon = () => {
    if (theme === 'light') return '☀️';
    if (theme === 'dark') return '🌙';
    return '🌓';
  };

  return (
    <button
      type="button"
      className="icon-btn"
      onClick={toggleTheme}
      title={`当前主题: ${theme} (点击切换)`}
    >
      <span style={{ fontSize: 13 }}>{getIcon()}</span>
    </button>
  );
};
