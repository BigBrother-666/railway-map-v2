/**
 * 读取 OAuth 回调跳转回来的 ?login=success|error（失败附 &reason=）参数，
 * 转成用户可读的提示文案，并清理地址栏（避免刷新重复弹提示）。
 */

export interface LoginResult {
  kind: 'success' | 'error';
  message: string;
}

/** reason 代码 → 中文提示，与后端 httpapi.Callback 的 redirectLogin 保持一致。 */
const REASON_MESSAGES: Record<string, string> = {
  'bad-state': '登录校验失败，请重试',
  'no-code': '登录未完成，请重试',
  'not-bound': '请先进入游戏并执行 /ticket weblogin bind 绑定账号',
  'login-failed': '登录失败，请重试',
  'session-failed': '会话创建失败，请重试',
};

/**
 * 若当前 URL 带有 login 参数则解析为登录结果并从地址栏移除；否则返回 null。
 * 只应在应用挂载时调用一次。
 */
export function consumeLoginResult(): LoginResult | null {
  if (typeof window === 'undefined') return null;
  const params = new URLSearchParams(window.location.search);
  const login = params.get('login');
  if (login !== 'success' && login !== 'error') return null;

  // 清理地址栏：移除 login/reason，保留其余参数与 hash。
  params.delete('login');
  const reason = params.get('reason');
  params.delete('reason');
  const query = params.toString();
  const cleanUrl = window.location.pathname + (query ? `?${query}` : '') + window.location.hash;
  window.history.replaceState(null, '', cleanUrl);

  if (login === 'success') {
    return { kind: 'success', message: '登录成功' };
  }
  return {
    kind: 'error',
    message: (reason && REASON_MESSAGES[reason]) || '登录失败，请重试',
  };
}
