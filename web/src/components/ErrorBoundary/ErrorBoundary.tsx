import { Component, ErrorInfo, ReactNode } from 'react';
import styles from './ErrorBoundary.module.css';

export interface ErrorBoundaryProps {
  children: ReactNode;
  level?: 'app' | 'route';
  resetKey?: any;
  onError?: (error: Error, errorInfo: ErrorInfo) => void;
  fallback?: ReactNode | ((error: Error, reset: () => void) => ReactNode);
}

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
  errorInfo: ErrorInfo | null;
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = {
      hasError: false,
      error: null,
      errorInfo: null,
    };
  }

  static getDerivedStateFromError(error: Error): Partial<ErrorBoundaryState> {
    return {
      hasError: true,
      error,
    };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    // 约束：边界要打 console.error，方便排查
    console.error(
      `[ErrorBoundary (${this.props.level || 'route'}) caught error]:`,
      error,
      errorInfo
    );
    this.setState({ errorInfo });
    this.props.onError?.(error, errorInfo);
  }

  componentDidUpdate(prevProps: ErrorBoundaryProps) {
    // 当 resetKey 发生变化时自动清除错误状态（例如路由切换时）
    if (this.state.hasError && this.props.resetKey !== prevProps.resetKey) {
      this.resetError();
    }
  }

  resetError = () => {
    const nextState = {
      hasError: false,
      error: null,
      errorInfo: null,
    };
    this.state = nextState;
    this.setState(nextState);
  };

  handleReload = () => {
    if (typeof window !== 'undefined') {
      window.location.reload();
    }
  };

  render() {
    const { hasError, error } = this.state;
    const { children, level = 'route', fallback } = this.props;

    if (!hasError) {
      return children;
    }

    if (typeof fallback === 'function') {
      return fallback(error || new Error('未知错误'), this.resetError);
    }
    if (fallback) {
      return fallback;
    }

    const isDev = Boolean(
      (typeof import.meta !== 'undefined' && import.meta.env?.DEV) ||
      (typeof process !== 'undefined' && process.env?.NODE_ENV === 'development')
    );
    const errorMsg = error?.message || '未知异常';
    const errorStack = error?.stack;

    // 应用级崩溃兜底：整页居中卡片，带刷新按钮与错误摘要，杜绝白屏
    if (level === 'app') {
      return (
        <div className={styles.appContainer}>
          <div className={styles.appCard} role="alert">
            <div className={styles.header}>
              <span className={styles.icon}>⚠️</span>
              <h2 className={styles.title}>系统出错了</h2>
            </div>
            <div className={styles.message}>
              {errorMsg}
            </div>
            {isDev && errorStack && (
              <pre className={styles.stack}>{errorStack}</pre>
            )}
            <div className={styles.actions}>
              <button
                type="button"
                className="btn primary"
                onClick={this.handleReload}
              >
                刷新页面
              </button>
              <button
                type="button"
                className="btn"
                onClick={this.resetError}
              >
                重试
              </button>
            </div>
          </div>
        </div>
      );
    }

    // 路由级崩溃：侧栏与顶栏保持完好，仅替换主内容区
    return (
      <div className={styles.routeContainer}>
        <div className={styles.routeCard} role="alert">
          <div className={styles.header}>
            <span className={styles.icon}>⚠️</span>
            <h3 className={styles.title}>当前页面加载出错</h3>
          </div>
          <div className={styles.message}>
            {errorMsg}
          </div>
          {isDev && errorStack && (
            <pre className={styles.stack}>{errorStack}</pre>
          )}
          <div className={styles.actions}>
            <button
              type="button"
              className="btn primary"
              onClick={this.resetError}
            >
              重试
            </button>
            <a
              href="/nodes"
              className="btn ghost"
              onClick={() => {
                // 如果使用 React Router，重置并回到总览
                this.resetError();
              }}
            >
              返回节点总览
            </a>
          </div>
        </div>
      </div>
    );
  }
}
