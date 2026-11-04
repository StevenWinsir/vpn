'use client';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { useCallback, useSyncExternalStore } from 'react';
import {
  Accordion,
  Alert,
  Anchor,
  Button,
  Container,
  Group,
  Paper,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import {
  IconBrandAndroid,
  IconBrandApple,
  IconBrandWindows,
  IconBulb,
  IconDeviceMobile,
  IconDownload,
  IconHeadset,
  IconInfoCircle,
  IconMessageCircleQuestion,
  IconAlertTriangle,
} from '@tabler/icons-react';
import { useAuth } from './providers';
import { useSupportOffered } from './support-widget';
import { openSupport } from '@/lib/support';
import { useMeta } from '@/lib/use-meta';
import {
  beforeYouStart,
  choosePlatform,
  faqFor,
  parseInline,
  platformIds,
  platforms,
  type GuideDownloads,
  type PlatformId,
  type Section,
  type Step,
} from '@/lib/guides';

const icons = {
  windows: IconBrandWindows,
  apple: IconBrandApple,
  android: IconBrandAndroid,
  phone: IconDeviceMobile,
} as const;

// The User-Agent is read after hydration only (the server renders the default), so the two
// renderings never disagree (handoff_guide.md 6.2).
const noSubscription = () => () => undefined;
const readAgent = () => navigator.userAgent;
const serverAgent = () => '';

function Rich({ text }: { text: string }) {
  return (
    <>
      {parseInline(text).map((part, index) => {
        if (part.type === 'strong') return <strong key={index}>{part.text}</strong>;
        if (part.type === 'link')
          return (
            <Anchor key={index} component={Link} href={part.href} inherit>
              {part.text}
            </Anchor>
          );
        return part.text;
      })}
    </>
  );
}

function DownloadAction({ id, downloads }: { id: PlatformId; downloads: GuideDownloads }) {
  const key = platforms[id].downloadKey;
  const version = key ? downloads[key]?.version : null;
  if (key && version)
    return (
      <Group gap="sm" mt="sm" data-testid="guide-download" data-available="true">
        <Button
          component={Link}
          href={`/download#${key}`}
          leftSection={<IconDownload size={18} />}
          size="sm"
        >
          前往下载
        </Button>
        <Text size="sm" c="dimmed">
          当前版本 {version}
        </Text>
      </Group>
    );
  return (
    <Alert
      mt="sm"
      color="gray"
      icon={<IconInfoCircle size={18} />}
      data-testid="guide-download"
      data-available="false"
    >
      安装包请联系客服获取
    </Alert>
  );
}

function StepItem({
  step,
  index,
  id,
  downloads,
}: {
  step: Step;
  index: number;
  id: PlatformId;
  downloads: GuideDownloads;
}) {
  return (
    <li className="guide-step">
      <span className="guide-step-no" aria-hidden>
        {index + 1}
      </span>
      <div className="guide-step-body">
        <Title order={3} size="h5">
          {step.title}
        </Title>
        <Text mt={4} size="sm" className="guide-text">
          <Rich text={step.body} />
        </Text>
        {step.download ? <DownloadAction id={id} downloads={downloads} /> : null}
      </div>
    </li>
  );
}

function SectionBlock({
  section,
  id,
  downloads,
}: {
  section: Section;
  id: PlatformId;
  downloads: GuideDownloads;
}) {
  return (
    <section id={section.id} className="guide-section" aria-labelledby={`${section.id}-title`}>
      <Title order={2} size="h3" id={`${section.id}-title`}>
        {section.title}
      </Title>
      <ol className="guide-steps">
        {section.steps.map((step, index) => (
          <StepItem key={step.title} step={step} index={index} id={id} downloads={downloads} />
        ))}
      </ol>
      {section.note ? (
        <Alert
          mt="md"
          color={section.note.kind === 'warn' ? 'orange' : 'teal'}
          icon={
            section.note.kind === 'warn' ? <IconAlertTriangle size={18} /> : <IconBulb size={18} />
          }
          data-testid="guide-note"
        >
          {section.note.text}
        </Alert>
      ) : null}
    </section>
  );
}

function Help() {
  const { user } = useAuth();
  const { meta } = useMeta();
  const chat = useSupportOffered();
  // Administrators have no tickets of their own (the sidebar hides the entry for them too).
  const tickets = meta?.tickets_enabled === true && user?.role !== 'admin';
  return (
    <Paper
      withBorder
      radius="lg"
      p="xl"
      id="help"
      className="guide-section"
      data-testid="guide-help"
    >
      <Title order={2} size="h3">
        仍未解决
      </Title>
      <Text mt="xs" size="sm" className="guide-text">
        先在客户端的“账户”或“我的”页点“复制诊断信息”，它不含密码和邮箱等个人信息；然后把它连同问题描述一起发给我们。
      </Text>
      <Group mt="md">
        {tickets ? (
          <Button
            component={Link}
            href="/tickets"
            variant="light"
            leftSection={<IconMessageCircleQuestion size={18} />}
          >
            提交工单
          </Button>
        ) : null}
        {chat ? (
          <Button variant="light" leftSection={<IconHeadset size={18} />} onClick={openSupport}>
            在线客服
          </Button>
        ) : null}
      </Group>
      {!tickets && !chat ? (
        <Text mt="sm" size="sm" c="dimmed">
          登录网站后，可以在左侧栏找到“我的工单”和“在线客服”。
        </Text>
      ) : null}
    </Paper>
  );
}

export function GuideView({ downloads }: { downloads: GuideDownloads }) {
  const router = useRouter();
  const params = useSearchParams();
  const agent = useSyncExternalStore(noSubscription, readAgent, serverAgent);
  const id = choosePlatform(params.get('platform'), agent);
  const platform = platforms[id];
  const choose = useCallback(
    (value: string) => router.replace(`/guide?platform=${value}`, { scroll: false }),
    [router],
  );
  return (
    <Container size="lg" py={48}>
      <Stack gap="xl">
        <div>
          <Title order={1}>使用教程</Title>
          <Text c="dimmed" mt="xs">
            跟着步骤做，几分钟就能用上 AsterLink。先选你的设备。
          </Text>
        </div>

        <section aria-labelledby="before-title">
          <Title order={2} size="h4" id="before-title" mb="sm">
            开始之前
          </Title>
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="md">
            {beforeYouStart.map((item, index) => (
              <Paper key={item.title} withBorder radius="lg" p="md" data-testid="guide-before">
                <Text fw={650} size="sm">
                  {index + 1}. {item.title}
                </Text>
                <Text mt={4} size="sm" c="dimmed">
                  <Rich text={item.body} />
                </Text>
              </Paper>
            ))}
          </SimpleGrid>
        </section>

        <SegmentedControl
          className="guide-seg"
          fullWidth
          size="md"
          value={id}
          onChange={choose}
          aria-label="选择设备"
          data={platformIds.map((value) => {
            const Icon = icons[platforms[value].icon];
            return {
              value,
              label: (
                <Group gap={6} justify="center" wrap="nowrap">
                  <Icon size={18} aria-hidden />
                  <span>{platforms[value].name}</span>
                </Group>
              ),
            };
          })}
        />

        <div className="guide-layout" data-testid="guide-platform" data-platform={id}>
          <nav className="guide-toc" aria-label="本页目录">
            <Text size="xs" fw={600} c="dimmed" mb="xs">
              {platform.name} 教程目录
            </Text>
            {[
              ...platform.sections.map((section) => ({ id: section.id, title: section.title })),
              { id: 'faq', title: '常见问题' },
              { id: 'help', title: '仍未解决' },
            ].map((item) => (
              <a key={item.id} href={`#${item.id}`}>
                {item.title}
              </a>
            ))}
          </nav>
          <div className="guide-main">
            <Paper withBorder radius="lg" p="lg" data-testid="guide-device">
              <Group gap="sm" wrap="nowrap" align="flex-start">
                {(() => {
                  const Icon = icons[platform.icon];
                  return <Icon size={28} stroke={1.5} aria-hidden />;
                })()}
                <div>
                  <Title order={2} size="h4">
                    {platform.name} 适用设备
                  </Title>
                  <Text size="sm" mt={2}>
                    {platform.device}
                  </Text>
                  <Text size="sm" c="dimmed" mt="xs">
                    {platform.sections[0].steps[0].body}
                  </Text>
                </div>
              </Group>
            </Paper>
            {platform.sections
              .filter((section) => section.id !== 'device')
              .map((section) => (
                <SectionBlock key={section.id} section={section} id={id} downloads={downloads} />
              ))}
            <section id="faq" className="guide-section" aria-labelledby="faq-title">
              <Title order={2} size="h3" id="faq-title" mb="sm">
                常见问题
              </Title>
              <Accordion variant="separated" radius="md" data-testid="guide-faq">
                {faqFor(id).map((item) => (
                  <Accordion.Item key={item.q} value={item.q}>
                    <Accordion.Control>{item.q}</Accordion.Control>
                    <Accordion.Panel>
                      <Text size="sm" className="guide-text">
                        <Rich text={item.a} />
                      </Text>
                    </Accordion.Panel>
                  </Accordion.Item>
                ))}
              </Accordion>
            </section>
            <Help />
          </div>
        </div>
      </Stack>
    </Container>
  );
}
