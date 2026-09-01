import { RefObject, useEffect, useRef, useState } from 'react';

/**
 * Runs `tick` on `intervalMs` only while:
 *   - the document is visible (Page Visibility API), AND
 *   - if `targetRef` is provided, the target element is actually rendered
 *     (offset dimensions > 0 — i.e. not in a `display: none` parent).
 *
 * Calls `tick` immediately when the conditions become true again, so a
 * backgrounded tab catches up the moment it's foregrounded.
 */
export const useVisibleInterval = (
  tick: () => void,
  intervalMs: number,
  options: { enabled?: boolean; targetRef?: RefObject<HTMLElement> } = {},
): void => {
  const { enabled = true, targetRef } = options;
  const tickRef = useRef(tick);
  useEffect(() => { tickRef.current = tick; }, [tick]);

  const [elementVisible, setElementVisible] = useState(true);
  useEffect(() => {
    if (!targetRef?.current || typeof IntersectionObserver === 'undefined') return;
    const el = targetRef.current;
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) setElementVisible(entry.isIntersecting);
      },
      { threshold: 0.01 },
    );
    observer.observe(el);
    setElementVisible(el.offsetParent !== null && el.offsetWidth > 0);
    return () => observer.disconnect();
  }, [targetRef]);

  useEffect(() => {
    if (!enabled) return;
    let timer: NodeJS.Timeout | null = null;
    const conditionsOk = () => document.visibilityState === 'visible' && elementVisible;
    const start = () => {
      if (timer) return;
      timer = setInterval(() => tickRef.current(), intervalMs);
    };
    const stop = () => {
      if (timer) {
        clearInterval(timer);
        timer = null;
      }
    };
    const evaluate = () => {
      if (conditionsOk()) {
        tickRef.current();
        start();
      } else {
        stop();
      }
    };
    if (conditionsOk()) start();
    const onVis = () => evaluate();
    document.addEventListener('visibilitychange', onVis);
    return () => {
      stop();
      document.removeEventListener('visibilitychange', onVis);
    };
  }, [intervalMs, enabled, elementVisible]);
};
