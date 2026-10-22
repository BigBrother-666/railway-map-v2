import { Monitor, Moon, Sun } from 'lucide-react';
import type { ThemeChoice } from '../theme';

const LABEL: Record<ThemeChoice, string> = { auto: '跟随系统', light: '浅色', dark: '深色' };
const NEXT: Record<ThemeChoice, ThemeChoice> = { auto: 'light', light: 'dark', dark: 'auto' };
const ICON = { auto: Monitor, light: Sun, dark: Moon };

export function ThemeToggle({ choice, onChange, className }: {
  choice: ThemeChoice;
  onChange: (choice: ThemeChoice) => void;
  className: string;
}) {
  const Icon = ICON[choice];
  const label = `主题：${LABEL[choice]}，点击切换为${LABEL[NEXT[choice]]}`;
  return (
    <button type="button" className={className} onClick={() => onChange(NEXT[choice])} title={label} aria-label={label}>
      <Icon size={18} aria-hidden="true" />
    </button>
  );
}
