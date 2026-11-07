'use client';
import Image from 'next/image';
import Link from 'next/link';
import { useState } from 'react';
import { useReducedMotion } from '@mantine/hooks';
import {
  Accordion,
  Alert,
  Anchor,
  Button,
  Container,
  Group,
  Modal,
  Paper,
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
  IconZoomIn,
} from '@tabler/icons-react';
import { useAuth } from './providers';
import { useSupportOffered } from './support-widget';
import { openSupport } from '@/lib/support';
import { useMeta } from '@/lib/use-meta';
import {
  beforeYouStart,
  faqFor,
  guidePath,
  parseInline,
  platformIds,
  platforms,
  type GuideDownloads,
  type Highlight,
  type PlatformId,
  type Section,
  type Step,
} from '@/lib/guides';
import { guideImages, type GuideImage } from '@/lib/guide-images';

export const icons = {
  windows: IconBrandWindows,
  apple: IconBrandApple,
  android: IconBrandAndroid,
  phone: IconDeviceMobile,
} as const;

export function Rich({ text }: { text: string }) {
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
  const version = downloads[key]?.version;
  if (version)
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

// The numbered marks over a picture: percent boxes, so they follow the picture's size. Screen
// readers skip them; the step text names the same numbers and says what to tap (handoff_guide.md 6.4).
function Marks({ highlights }: { highlights: Highlight[] }) {
  return (
    <>
      {highlights.map((mark) => (
        <span
          key={mark.n}
          className="guide-mark"
          data-shape={mark.shape ?? 'box'}
          data-badge={mark.badge ?? 'start'}
          data-n={mark.n}
          aria-hidden
          style={{
            left: `${mark.rect[0]}%`,
            top: `${mark.rect[1]}%`,
            width: `${mark.rect[2]}%`,
            height: `${mark.rect[3]}%`,
          }}
        >
          <span className="guide-mark-no">{mark.n}</span>
        </span>
      ))}
    </>
  );
}

// The zoom shows a phone about one and a half times its step size (it still fits a laptop
// screen's height) and anything else at its own size, at most 960 wide.
const zoomWidth = (image: GuideImage) =>
  image.device === 'phone' ? 420 : Math.max(image.display, Math.min(image.width, 960));

function Shot({ image, step }: { image: GuideImage; step: Step }) {
  const [open, setOpen] = useState(false);
  const reduceMotion = useReducedMotion();
  const highlights = step.highlights ?? [];
  return (
    <figure className="guide-shot" data-device={image.device} data-testid="guide-shot">
      <button
        type="button"
        className="guide-shot-frame"
        onClick={() => setOpen(true)}
        aria-label={`放大查看图片：${image.alt}`}
        style={{ maxWidth: image.display }}
      >
        <Image
          src={image.file}
          width={image.width}
          height={image.height}
          alt={image.alt}
          unoptimized
          loading="lazy"
          sizes={image.device === 'phone' ? '280px' : '(max-width: 767px) 100vw, 860px'}
        />
        <Marks highlights={highlights} />
        <span className="guide-shot-zoom" aria-hidden>
          <IconZoomIn size={16} />
        </span>
      </button>
      <figcaption>示意图，线路名称与数据以实际为准</figcaption>
      <Modal
        opened={open}
        onClose={() => setOpen(false)}
        title={step.title}
        size="auto"
        centered
        closeButtonProps={{ 'aria-label': '关闭' }}
        transitionProps={reduceMotion ? { duration: 0 } : undefined}
        data-testid="guide-zoom"
      >
        <div
          className="guide-zoom-frame"
          data-device={image.device}
          style={{ maxWidth: zoomWidth(image) }}
        >
          <Image
            src={image.file}
            width={image.width}
            height={image.height}
            alt={image.alt}
            unoptimized
          />
          <Marks highlights={highlights} />
        </div>
        <Text size="sm" c="dimmed" mt="sm" className="guide-text">
          {image.alt}
        </Text>
      </Modal>
    </figure>
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
  // A step shows its picture once the manifest has it (pictures arrive per platform).
  const image = step.shot ? guideImages[step.shot] : undefined;
  return (
    <li className="guide-step">
      <span className="guide-step-no" aria-hidden>
        {index + 1}
      </span>
      <div className="guide-step-body" data-device={image?.device}>
        <div className="guide-step-text">
          <Title order={3} size="h5">
            {step.title}
          </Title>
          <Text mt={4} size="sm" className="guide-text">
            <Rich text={step.body} />
          </Text>
          {step.download ? <DownloadAction id={id} downloads={downloads} /> : null}
        </div>
        {image ? <Shot image={image} step={step} /> : null}
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

/** The four things to have ready (handoff_guide.md 4), on the overview and every platform page. */
export function BeforeYouStart() {
  return (
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
  );
}

// The device switch is four plain links (handoff_seo.md 6.1): each platform has its own address,
// so the browser's back and forward buttons step through the platforms a visitor looked at. The
// scroll position stays, as it did when the switch rewrote the query.
function PlatformSwitch({ id }: { id: PlatformId }) {
  return (
    <nav className="guide-seg" aria-label="选择设备">
      {platformIds.map((value) => {
        const Icon = icons[platforms[value].icon];
        return (
          <Link
            key={value}
            href={guidePath(value)}
            scroll={false}
            aria-current={value === id ? 'page' : undefined}
            data-platform={value}
          >
            <Icon size={18} aria-hidden />
            <span>{platforms[value].name}</span>
          </Link>
        );
      })}
    </nav>
  );
}

/** One platform's guide (/guide/{platform}): the server knows the platform from the address. */
export function GuideView({ id, downloads }: { id: PlatformId; downloads: GuideDownloads }) {
  const platform = platforms[id];
  return (
    <Container size="lg" py={48}>
      <Stack gap="xl">
        <div>
          <nav aria-label="当前位置" className="guide-crumbs" data-testid="guide-crumbs">
            <Link href="/guide">使用教程</Link>
            <span aria-hidden>/</span>
            <span aria-current="page">{platform.name}</span>
          </nav>
          <Title order={1} mt={6}>
            {platform.name} 使用教程
          </Title>
          <Text c="dimmed" mt="xs">
            跟着步骤做，几分钟就能在 {platform.name} 上用上
            AsterLink。用的是其他设备，在下方切换即可。
          </Text>
        </div>

        <BeforeYouStart />

        <PlatformSwitch id={id} />

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
