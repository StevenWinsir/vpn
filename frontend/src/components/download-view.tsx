'use client';
import { useSyncExternalStore } from 'react';
import Link from 'next/link';
import {
  Alert,
  Anchor,
  Badge,
  Button,
  Code,
  Container,
  Group,
  List,
  Paper,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import {
  IconBrandAndroid,
  IconBrandApple,
  IconBrandWindows,
  IconDownload,
  IconInfoCircle,
  IconTerminal2,
} from '@tabler/icons-react';
import {
  grouped,
  iosInstallLink,
  platformOrder,
  platformView,
  type DownloadState,
  type PlatformId,
  type PlatformView,
} from '@/lib/downloads';

// The download page (handoff_download.md 6.2): five platform cards in a fixed order, then how to
// install, upgrade and remove. Every button points at this site's own path; no file address is
// in the page or its data (D4).
const cards: Record<
  PlatformId,
  { title: string; requirement: string | null; icon: typeof IconBrandApple; guide: string | null }
> = {
  windows: {
    title: 'Windows',
    requirement: 'Windows 10（2004）及以上或 Windows 11',
    icon: IconBrandWindows,
    guide: 'windows',
  },
  macos: {
    title: 'macOS',
    requirement: 'macOS 15 及以上',
    icon: IconBrandApple,
    guide: 'macos',
  },
  android: {
    title: 'Android',
    requirement: 'Android 8.0 及以上',
    icon: IconBrandAndroid,
    guide: 'android',
  },
  ios: {
    title: 'iPhone',
    requirement: 'iOS 17 及以上 · iPhone',
    icon: IconBrandApple,
    guide: 'ios',
  },
  linux: { title: 'Linux', requirement: null, icon: IconTerminal2, guide: null },
};

const noop = () => () => {};

// The page's own origin as the browser sees it, for the iPhone install link (an absolute address is
// required there). Before hydration the button points at the card instead.
function useOrigin(): string | null {
  return useSyncExternalStore(
    noop,
    () => window.location.origin,
    () => null,
  );
}

function Missing({ state, platform }: { state: DownloadState; platform: PlatformId }) {
  const text =
    state.status === 'unavailable'
      ? '下载信息暂时无法获取，请稍后刷新。'
      : platform === 'linux'
        ? 'Linux 版本即将推出，敬请期待。'
        : `${cards[platform].title} 版本暂未发布，请稍后再来。`;
  return (
    <Alert
      mt="lg"
      color="gray"
      icon={<IconInfoCircle size={18} />}
      data-testid="download-missing"
      data-reason={state.status === 'unavailable' ? 'unavailable' : 'unpublished'}
    >
      {text}
    </Alert>
  );
}

function PlatformCard({ state, view }: { state: DownloadState; view: PlatformView }) {
  const card = cards[view.platform];
  const Icon = card.icon;
  const origin = useOrigin();
  return (
    <Paper
      withBorder
      radius="lg"
      p="xl"
      id={view.platform}
      className="guide-section"
      data-testid="download-platform"
      data-platform={view.platform}
    >
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="md">
        <Group gap="md" align="center">
          <Icon size={40} stroke={1.5} />
          <div>
            <Title order={2} size="h3">
              {card.title}
            </Title>
            {card.requirement ? (
              <Text size="sm" c="dimmed">
                {card.requirement}
              </Text>
            ) : null}
          </div>
        </Group>
        {view.badge ? (
          <Badge variant="light" size="lg" data-testid="download-version">
            版本 {view.badge}
          </Badge>
        ) : null}
      </Group>

      {view.buttons.length > 0 ? (
        <Stack gap="md" mt="lg">
          <Group gap="sm" align="center">
            {view.buttons.map((b) => (
              <Group key={b.key} gap={6} align="center" maw="100%">
                <Button
                  component="a"
                  href={b.ios ? (origin ? iosInstallLink(origin) : `#${view.platform}`) : b.href}
                  // Crawlers stay off the file redirects (handoff_seo.md D10).
                  rel="nofollow"
                  variant={b.solid ? 'filled' : 'light'}
                  leftSection={<IconDownload size={18} />}
                  size="md"
                  data-testid="download-button"
                  data-file={b.key}
                  // Long labels wrap on a phone instead of being cut off (handoff_download.md DL3-11).
                  styles={{
                    root: { maxWidth: '100%', height: 'auto', minHeight: 42, paddingBlock: 8 },
                    inner: { height: 'auto' },
                    label: { whiteSpace: 'normal', overflow: 'visible', lineHeight: 1.4 },
                  }}
                >
                  {b.label}
                </Button>
                {b.version ? (
                  <Text size="sm" c="dimmed" data-testid="download-button-version">
                    版本 {b.version}
                  </Text>
                ) : null}
              </Group>
            ))}
          </Group>
          {view.hasIpa ? (
            <Text size="sm" c="dimmed">
              请用 iPhone 上的 Safari 打开本页并点击“在 iPhone
              上安装”；系统询问“是否安装”时选择“安装”。安装后首次打开，如提示“未受信任的开发者”，到“设置
              → 通用 → VPN 与设备管理”中信任该开发者。仅限已登记的设备安装。
            </Text>
          ) : null}
          {view.store === 'app_store' ? (
            <Text size="sm" c="dimmed">
              点击后会打开 App Store，在其中下载安装即可。
            </Text>
          ) : null}
          {view.store === 'testflight' ? (
            <Text size="sm" c="dimmed">
              请先在 iPhone 上从 App Store 安装 TestFlight，再点击按钮接受邀请并安装。
            </Text>
          ) : null}
          {view.files.map((f) => (
            <div key={f.key} data-testid="download-sha">
              <Text size="sm" fw={600}>
                {f.title}
              </Text>
              <Code block style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>
                {grouped(f.sha256)}
              </Code>
            </div>
          ))}
          {view.certSha256 ? (
            <div>
              <Text size="sm" fw={600}>
                签名证书 SHA-256（每个版本都相同；不同即不是官方安装包）
              </Text>
              <Code block style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>
                {grouped(view.certSha256)}
              </Code>
            </div>
          ) : null}
        </Stack>
      ) : (
        <Missing state={state} platform={view.platform} />
      )}
      {card.guide ? (
        <Text size="sm" mt="md">
          安装后不会用？
          <Anchor
            component={Link}
            href={`/guide?platform=${card.guide}`}
            size="sm"
            data-testid="download-guide-link"
          >
            查看使用教程
          </Anchor>
        </Text>
      ) : null}
    </Paper>
  );
}

function HowTo({
  title,
  guide,
  children,
}: {
  title: string;
  guide: string;
  children: React.ReactNode;
}) {
  return (
    <Paper withBorder radius="lg" p="xl" data-testid="download-howto">
      <Title order={2} size="h4" mb="sm">
        {title}
      </Title>
      <Text size="sm" c="dimmed" mb="sm">
        详细图文步骤见
        <Anchor component={Link} href={`/guide?platform=${guide}`} size="sm">
          使用教程
        </Anchor>
        。
      </Text>
      <List spacing="xs" size="sm">
        {children}
      </List>
    </Paper>
  );
}

export function DownloadView({ state }: { state: DownloadState }) {
  return (
    <Container size="md" py={48}>
      <Stack gap="xl">
        <div>
          <Title order={1}>下载客户端</Title>
          <Text c="dimmed" mt="xs">
            使用网站账户登录即可连接，套餐、线路与流量在各设备间实时同步。
          </Text>
        </div>

        {platformOrder.map((platform) => (
          <PlatformCard key={platform} state={state} view={platformView(state.entries, platform)} />
        ))}

        <HowTo title="Windows 安装、升级与卸载" guide="windows">
          <List.Item>
            首次安装：双击运行安装程序。如果出现“Windows
            已保护你的电脑”，点“更多信息”，再点“仍要运行”；出现“用户账户控制”提示时点“是”。
          </List.Item>
          <List.Item>
            升级：先从任务栏通知区域的 AsterLink
            图标选“退出”，再运行新版本的安装程序覆盖安装，记住的密码与设置会保留。
          </List.Item>
          <List.Item>
            卸载：打开“设置”，进入“应用”，找到 AsterLink
            点“卸载”。询问是否删除保存的密码和偏好时，默认选“否”。
          </List.Item>
        </HowTo>

        <HowTo title="macOS 安装、升级与卸载" guide="macos">
          <List.Item>
            首次安装：打开 DMG，把 AsterLink
            拖进“应用程序”，再从“应用程序”文件夹里打开。如果系统提示无法验证、阻止打开，到“系统设置
            → 隐私与安全性”中点“仍要打开”。
          </List.Item>
          <List.Item>
            升级：先断开连接，用新版本替换“应用程序”里的
            AsterLink，再打开。如果提示“后台服务需要更新”，到“账户”页按提示处理。
          </List.Item>
          <List.Item>
            卸载：先到“账户”页的“后台服务”部分点“移除后台服务”，再使用安装包里附带的卸载程序。
          </List.Item>
        </HowTo>

        <HowTo title="Android 安装、升级与卸载" guide="android">
          <List.Item>
            首次安装：用手机浏览器下载 APK 后点击打开；系统提示“未知来源”时，允许该浏览器安装应用。
          </List.Item>
          <List.Item>
            首次连接时系统会询问是否允许建立 VPN 连接，选择“允许”即可；不需要 root。
          </List.Item>
          <List.Item>
            升级：直接下载新版本覆盖安装，记住的密码与设置会保留。系统不允许安装比当前更旧的版本。
          </List.Item>
          <List.Item>
            卸载：在系统设置中卸载应用即可，VPN 配置、记住的密码和设备标识会一并删除。
          </List.Item>
          <List.Item>
            若手机提示“应用版本过低”，回到本页下载最新版本。部分手机的省电策略会在后台限制或关闭
            VPN，表现为网速变慢或断开，可在应用“我的 →
            后台运行与省电”中按提示设置，各品牌手机的放行步骤见使用教程。
          </List.Item>
        </HowTo>

        <HowTo title="iPhone 安装、升级与卸载" guide="ios">
          <List.Item>
            安装：用 iPhone 的 Safari 打开本页，点击“在 iPhone 上安装”。首次连接时系统会询问是否添加
            VPN 配置，选择“允许”（可能要求输入锁屏密码）。
          </List.Item>
          <List.Item>升级：回到本页再次安装新版本即可覆盖，记住的密码与 VPN 配置会保留。</List.Item>
          <List.Item>
            卸载：长按图标删除应用，VPN
            配置会一并移除；记住的密码随应用数据清除，设备标识可能保留在系统钥匙串中。
          </List.Item>
          <List.Item>
            若 VPN 在后台因额度或套餐到期断开，会以通知提醒；请在“设置 → 通知”中保持 AsterLink
            的通知开启。
          </List.Item>
        </HowTo>
      </Stack>
    </Container>
  );
}
