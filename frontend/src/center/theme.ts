/**
 * 个人中心明暗主题：light / dark / auto（跟随系统，默认）。只作用于个人中心，不影响地图页。
 */
import { useEffect, useState } from 'react';

export type ThemeChoice = 'auto' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

const STORAGE_KEY = 'bcts.theme';

export function loadThemeChoice(): ThemeChoice {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === 'light' || v === 'dark' || v === 'auto') return v;
  } catch {
    // 隐私模式等场景 localStorage 不可用，回退默认
  }
  return 'auto';
}

function saveThemeChoice(choice: ThemeChoice) {
  try {
    localStorage.setItem(STORAGE_KEY, choice);
  } catch {
    // 忽略：仅影响下次打开时的默认值
  }
}

/** 把选择解析为实际主题。 */
export function resolveTheme(choice: ThemeChoice, systemDark: boolean): ResolvedTheme {
  if (choice === 'auto') return systemDark ? 'dark' : 'light';
  return choice;
}

function systemPrefersDark(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches;
}

/** 当前主题选择与解析结果；auto 时监听系统主题变化。 */
export function useTheme() {
  const [choice, setChoiceState] = useState<ThemeChoice>(loadThemeChoice);
  const [systemDark, setSystemDark] = useState(systemPrefersDark);

  useEffect(() => {
    if (typeof matchMedia !== 'function') return;
    const mq = matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => setSystemDark(mq.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);

  const setChoice = (c: ThemeChoice) => {
    setChoiceState(c);
    saveThemeChoice(c);
  };

  return { choice, setChoice, theme: resolveTheme(choice, systemDark) };
}
