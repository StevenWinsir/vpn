'use client';
import { useEffect, useState } from 'react';
import { Burger } from '@mantine/core';
import { Logo } from '../site-shell';
import { ThemeToggle } from '../theme-toggle';
import { HeaderActions } from './home-actions';

export const homeSections = [
  { id: 'features', label: '产品能力' },
  { id: 'pricing', label: '套餐价格' },
  { id: 'clients', label: '客户端' },
  { id: 'faq', label: '常见问题' },
];

// The marketing header (handoff_ui.md 8.2): sticky, transparent over the hero and solid once the
// page scrolls; on narrow screens the anchors and actions fold into a menu.
export function HomeHeader() {
  const [scrolled, setScrolled] = useState(false);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const update = () => setScrolled(window.scrollY > 8);
    update();
    window.addEventListener('scroll', update, { passive: true });
    return () => window.removeEventListener('scroll', update);
  }, []);
  useEffect(() => {
    if (!open) return;
    const close = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    const wide = window.matchMedia('(min-width: 900px)');
    const reset = () => wide.matches && setOpen(false);
    window.addEventListener('keydown', close);
    wide.addEventListener('change', reset);
    return () => {
      window.removeEventListener('keydown', close);
      wide.removeEventListener('change', reset);
    };
  }, [open]);
  const done = () => setOpen(false);
  return (
    <header className="home-header" data-scrolled={scrolled || open ? '' : undefined}>
      <div className="home-header-inner">
        <Logo />
        <nav className="home-nav" aria-label="首页导航">
          {homeSections.map(({ id, label }) => (
            <a key={id} href={`#${id}`}>
              {label}
            </a>
          ))}
        </nav>
        <div className="home-header-tools">
          <ThemeToggle />
          <div className="home-header-actions">
            <HeaderActions />
          </div>
          <Burger
            className="home-burger"
            opened={open}
            onClick={() => setOpen((value) => !value)}
            size="sm"
            aria-label={open ? '收起菜单' : '展开菜单'}
            aria-expanded={open}
            aria-controls="home-menu"
          />
        </div>
      </div>
      {open ? (
        <div className="home-menu" id="home-menu">
          <nav aria-label="首页导航（菜单）">
            {homeSections.map(({ id, label }) => (
              <a key={id} href={`#${id}`} onClick={done}>
                {label}
              </a>
            ))}
          </nav>
          <div className="home-menu-actions">
            <HeaderActions onNavigate={done} />
          </div>
        </div>
      ) : null}
    </header>
  );
}
