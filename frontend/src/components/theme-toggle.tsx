'use client';
import { ActionIcon, Menu, useMantineColorScheme, type MantineColorScheme } from '@mantine/core';
import { IconCheck, IconDeviceDesktop, IconMoon, IconSun } from '@tabler/icons-react';

const options: { value: MantineColorScheme; label: string; icon: typeof IconSun }[] = [
  { value: 'light', label: '浅色', icon: IconSun },
  { value: 'dark', label: '深色', icon: IconMoon },
  { value: 'auto', label: '跟随系统', icon: IconDeviceDesktop },
];

// Light, dark or follow the system (handoff_ui.md 6.3, D7). The server cannot know the scheme, so
// the button renders both icons and CSS on the html attribute shows one: no hydration mismatch.
export function ThemeToggle() {
  return (
    <Menu shadow="md" width={168} position="bottom-end">
      <Menu.Target>
        <ActionIcon variant="default" size={40} radius="md" aria-label="切换外观">
          <IconSun size={19} stroke={1.6} className="mantine-dark-hidden" aria-hidden />
          <IconMoon size={19} stroke={1.6} className="mantine-light-hidden" aria-hidden />
        </ActionIcon>
      </Menu.Target>
      <Menu.Dropdown>
        <ThemeOptions />
      </Menu.Dropdown>
    </Menu>
  );
}

// Mounted only while the menu is open, that is in the browser, where the stored choice is known.
function ThemeOptions() {
  const { colorScheme, setColorScheme } = useMantineColorScheme();
  return options.map(({ value, label, icon: Icon }) => (
    <Menu.Item
      key={value}
      onClick={() => setColorScheme(value)}
      leftSection={<Icon size={16} stroke={1.6} />}
      rightSection={colorScheme === value ? <IconCheck size={15} aria-hidden /> : null}
      aria-current={colorScheme === value ? 'true' : undefined}
      data-scheme={value}
    >
      {label}
    </Menu.Item>
  ));
}
