'use client';
import Link from 'next/link';
import { useSyncExternalStore } from 'react';
import { Badge, Button, Container, Paper, SimpleGrid, Stack, Text, Title } from '@mantine/core';
import { IconArrowRight } from '@tabler/icons-react';
import { BeforeYouStart, icons } from './guide-view';
import {
  detectPlatform,
  guidePath,
  platformIds,
  platforms,
  type GuideDownloads,
  type PlatformId,
} from '@/lib/guides';

// The User-Agent is read after hydration only: the server renders the same overview for every
// visitor and every crawler (handoff_seo.md D5, no cloaking); the browser then marks the card of
// the visitor's own device and offers its guide first.
const noSubscription = () => () => undefined;
const readAgent = (): PlatformId | null => detectPlatform(navigator.userAgent);
const serverAgent = (): PlatformId | null => null;

/** The guide overview (/guide): what to have ready, and one entry per platform. */
export function GuideHub({ downloads }: { downloads: GuideDownloads }) {
  const recommended = useSyncExternalStore(noSubscription, readAgent, serverAgent);
  return (
    <Container size="lg" py={48}>
      <Stack gap="xl">
        <div>
          <Title order={1}>使用教程</Title>
          <Text c="dimmed" mt="xs">
            跟着步骤做，几分钟就能用上 AsterLink。先选你的设备。
          </Text>
          {/* The slot keeps its height before the browser knows the device, so nothing moves. */}
          <div className="guide-hub-next" data-testid="guide-recommend">
            {recommended ? (
              <Button
                component={Link}
                href={guidePath(recommended)}
                rightSection={<IconArrowRight size={17} />}
              >
                继续阅读 {platforms[recommended].name} 教程
              </Button>
            ) : null}
          </div>
        </div>

        <BeforeYouStart />

        <section aria-labelledby="platforms-title">
          <Title order={2} size="h4" id="platforms-title" mb="sm">
            选择你的设备
          </Title>
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="md">
            {platformIds.map((id) => {
              const platform = platforms[id];
              const Icon = icons[platform.icon];
              const version = downloads[platform.downloadKey]?.version;
              const mine = recommended === id;
              return (
                <Paper
                  key={id}
                  withBorder
                  radius="lg"
                  p="lg"
                  className="guide-hub-card"
                  data-testid="guide-hub-card"
                  data-platform={id}
                  data-recommended={mine ? 'true' : undefined}
                >
                  <Icon size={28} stroke={1.5} aria-hidden />
                  <Title order={3} size="h4" mt="sm">
                    {platform.name}
                  </Title>
                  <Text size="sm" c="dimmed" mt={4} className="guide-hub-device">
                    {platform.device}
                  </Text>
                  <div className="guide-hub-badges">
                    {version ? (
                      <Badge variant="light" radius="sm">
                        当前版本 {version}
                      </Badge>
                    ) : null}
                    {mine ? (
                      <Badge variant="filled" radius="sm">
                        你的设备
                      </Badge>
                    ) : null}
                  </div>
                  <Link className="guide-hub-link" href={guidePath(id)}>
                    查看 {platform.name} 教程
                    <IconArrowRight size={16} aria-hidden />
                  </Link>
                </Paper>
              );
            })}
          </SimpleGrid>
        </section>
      </Stack>
    </Container>
  );
}
