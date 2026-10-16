'use client';
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
import { IconBrandAndroid, IconDownload, IconInfoCircle } from '@tabler/icons-react';
import { grouped, megabytes, type AndroidDownload } from '@/lib/downloads';

export function DownloadView({ android }: { android: AndroidDownload | null }) {
  return (
    <Container size="md" py={48}>
      <Stack gap="xl">
        <div>
          <Title order={1}>下载客户端</Title>
          <Text c="dimmed" mt="xs">
            登录网站上注册的账号即可使用；套餐、线路与流量以账户为准，客户端只负责连接。
          </Text>
        </div>

        <Paper withBorder radius="lg" p="xl">
          <Group justify="space-between" align="flex-start" wrap="wrap" gap="md">
            <Group gap="md" align="center">
              <IconBrandAndroid size={40} stroke={1.5} />
              <div>
                <Title order={2} size="h3">
                  Android
                </Title>
                <Text size="sm" c="dimmed">
                  Android 8.0 及以上 · 64 位 ARM 手机（arm64-v8a）
                </Text>
              </div>
            </Group>
            {android ? (
              <Badge variant="light" size="lg">
                版本 {android.version}
              </Badge>
            ) : null}
          </Group>

          {android ? (
            <Stack gap="md" mt="lg">
              <Group>
                <Button
                  component="a"
                  href={android.url}
                  leftSection={<IconDownload size={18} />}
                  size="md"
                >
                  下载 APK{android.sizeBytes ? `（${megabytes(android.sizeBytes)}）` : ''}
                </Button>
              </Group>
              <div>
                <Text size="sm" fw={600}>
                  SHA-256（下载后可核对文件完整性）
                </Text>
                <Code block>{grouped(android.sha256)}</Code>
              </div>
              {android.certSha256 ? (
                <div>
                  <Text size="sm" fw={600}>
                    签名证书 SHA-256（每个版本都相同；不同即不是官方安装包）
                  </Text>
                  <Code block>{grouped(android.certSha256)}</Code>
                </div>
              ) : null}
            </Stack>
          ) : (
            <Alert mt="lg" color="gray" icon={<IconInfoCircle size={18} />}>
              Android 安装包暂未发布，请稍后再来。
            </Alert>
          )}
        </Paper>

        <Paper withBorder radius="lg" p="xl">
          <Title order={2} size="h4" mb="sm">
            安装、升级与卸载
          </Title>
          <List spacing="xs" size="sm">
            <List.Item>
              首次安装：用手机浏览器下载 APK
              后点击打开；系统提示“未知来源”时，允许该浏览器安装应用。
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
              若手机提示“应用版本过低”，回到本页下载最新版本。部分手机的省电策略会在后台关闭
              VPN，可在应用“我的 → 后台运行与省电”中按提示设置。
            </List.Item>
          </List>
        </Paper>

        <Paper withBorder radius="lg" p="xl">
          <Title order={2} size="h4" mb="sm">
            开源许可与源代码
          </Title>
          <Text size="sm" c="dimmed">
            客户端内置 Mihomo 代理内核（GNU GPL v3），并使用 AndroidX、Kotlin 等开源组件（Apache 2.0
            等许可）。每个版本的完整许可文本（THIRD_PARTY_LICENSES.txt）、依赖清单与对应源代码随安装包一同发布
            {android?.sourceUrl ? (
              <>
                ，可在
                <Anchor href={android.sourceUrl} size="sm">
                  这里获取
                </Anchor>
              </>
            ) : (
              '，如需获取请联系客服'
            )}
            。
          </Text>
        </Paper>
      </Stack>
    </Container>
  );
}
