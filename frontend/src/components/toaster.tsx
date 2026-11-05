'use client';
import { useEffect } from 'react';
import { Notifications } from '@mantine/notifications';
import { useReducedMotion } from '@mantine/hooks';
import { showNextPageToast, toastDurations } from '@/lib/toast';

// The one message container (handoff_message.md 4.2–4.3): top centre, three at a time (later ones
// wait), above dialogs. It lives in the root providers, so client-side navigation keeps a message
// on screen, and on start it shows the message a previous page left for this one.
export function Toaster() {
  const reduce = useReducedMotion();
  useEffect(() => {
    showNextPageToast();
  }, []);
  return (
    <Notifications
      position="top-center"
      limit={3}
      autoClose={toastDurations.success}
      containerWidth={420}
      transitionDuration={reduce ? 0 : 250}
      classNames={{ root: 'al-toasts', notification: 'al-toast' }}
    />
  );
}
