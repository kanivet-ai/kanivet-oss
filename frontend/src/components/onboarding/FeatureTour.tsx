import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { createPortal } from 'react-dom';
import { Cross2Icon } from '@radix-ui/react-icons';
import {
  markTourSeen,
  placeCard,
  seenTours,
  type Placement,
  type Rect,
} from './tour';
import './FeatureTour.css';

/**
 * A one-time guided tour of a new feature. It waits until the element its
 * first step points at is on screen (a cluster is open), shows a short
 * welcome, then walks through a few steps with a spotlight on the real UI.
 * Finishing, skipping or dismissing marks it seen for good; the id is what
 * makes it show once, whichever build (release candidate or release) first
 * contains it.
 */

interface Step {
  /** CSS selectors to spotlight, first match wins; none: centred card. */
  target?: string[];
  /** The step applies only while this selector is on screen; a step without
   * it always applies. This is how the tour follows what the cluster has:
   * metrics, a report still computing, or results. */
  when?: string;
  /** ...and only while this one is not. */
  unless?: string;
  title: string;
  body: React.ReactNode;
  /** Run when leaving this step forwards, e.g. to open the view the next
   * step points into. */
  onNext?: () => void;
}

const TOUR_ID = 'rightsizing-v1';
const START_SELECTOR = '[data-tour="rightsizing-nav"]';
const IN_DASHBOARD = '[data-tour="rs-dashboard"]';
const SETUP = `${IN_DASHBOARD} [data-tour="rs-setup"]`;
const PROGRESS = `${IN_DASHBOARD} [data-tour="rs-progress"]`;
const RESULTS = '[data-tour="rs-stats"]';

const STEPS: Step[] = [
  {
    title: 'New: Rightsizing',
    body: (
      <>
        <p>
          See which workloads ask for too much or too little CPU and memory, and
          what fixing it is worth, with the evidence behind every number.
        </p>
        <p className="ft-muted">
          Recommendations only: Kanivet never changes your cluster.
        </p>
      </>
    ),
  },
  {
    target: [START_SELECTOR],
    title: 'Every cluster has it',
    body: <p>Rightsizing sits in each cluster's sidebar, next to FinOps.</p>,
    onNext: () =>
      (document.querySelector(START_SELECTOR) as HTMLElement | null)?.click(),
  },
  {
    // No usable metrics: Prometheus or Mimir not found, Mimir without a
    // tenant, or a store with no container metrics for this cluster.
    target: [SETUP],
    when: SETUP,
    title: 'First, connect your metrics',
    body: (
      <>
        <p>
          Recommendations come from at least 3 days of CPU and memory history in
          Prometheus, Thanos, VictoriaMetrics or Mimir. Kanivet couldn't find
          usable history in this cluster on its own; the card says what it
          found.
        </p>
        <p>
          Connect it with the button on the card; the first report starts
          computing straight away.
        </p>
      </>
    ),
  },
  {
    target: [PROGRESS],
    when: PROGRESS,
    unless: RESULTS,
    title: 'Your first report is on its way',
    body: (
      <p>
        Kanivet is reading up to two weeks of usage history. On a large cluster
        the first run takes a minute or two; after that, refreshes only fetch
        the latest day. When it's done, this view fills with what you could
        save, what's at risk, and the evidence for every workload.
      </p>
    ),
  },
  {
    target: [RESULTS],
    when: RESULTS,
    title: 'Start with the big picture',
    body: (
      <p>
        What you could save, what's at risk of OOM kills or throttling, and the
        track record: how often past recommendations held up on days they never
        saw.
      </p>
    ),
  },
  {
    target: ['[data-tour="rs-filters"]'],
    when: '[data-tour="rs-filters"]',
    title: 'Find what needs doing',
    body: (
      <p>
        Filter by what to fix, or find your own workloads by namespace, release
        or team. Press <kbd>/</kbd> to search.
      </p>
    ),
  },
  {
    target: ['[data-tour="rs-profile"]'],
    when: RESULTS,
    title: 'Choose your headroom',
    body: (
      <p>
        Balanced suits most services. Conservative leaves more room for critical
        ones; Aggressive trims harder.
      </p>
    ),
  },
  {
    target: ['.rs-table .rs-row:not(.rs-head)'],
    when: '.rs-table .rs-row:not(.rs-head)',
    title: 'Open any workload',
    body: (
      <p>
        See a typical day of its CPU, the trade-off behind each request, and
        YAML ready to copy into Helm, GitOps or kubectl.
      </p>
    ),
  },
];

const applies = (s: Step) =>
  (!s.when || visibleRect([s.when]) !== null) &&
  (!s.unless || visibleRect([s.unless]) === null);

/** The next step after i that applies now, or -1. */
const nextApplicable = (i: number) => {
  for (let k = i + 1; k < STEPS.length; k++) if (applies(STEPS[k])) return k;
  return -1;
};

const visibleRect = (selectors?: string[]): Rect | null => {
  for (const sel of selectors ?? []) {
    const el = document.querySelector(sel) as HTMLElement | null;
    if (!el) continue;
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height > 0)
      return { top: r.top, left: r.left, width: r.width, height: r.height };
  }
  return null;
};

const storage = (): Storage | null => {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
};

let shownThisSession = false;

export const FeatureTour: React.FC = () => {
  const [step, setStep] = useState<number | null>(null);
  const [rect, setRect] = useState<Rect | null>(null);
  const [place, setPlace] = useState<Placement | null>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const primaryRef = useRef<HTMLButtonElement>(null);

  // Wait for a cluster to be open (the sidebar entry on screen), then a
  // moment more so the tour doesn't land on top of the first load.
  useEffect(() => {
    if (shownThisSession || seenTours(storage()).includes(TOUR_ID)) return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const check = () => {
      if (timer || !visibleRect([START_SELECTOR])) return;
      timer = setTimeout(() => {
        if (shownThisSession || !visibleRect([START_SELECTOR])) {
          timer = null;
          return;
        }
        shownThisSession = true;
        setStep(0);
        obs.disconnect();
      }, 1500);
    };
    const obs = new MutationObserver(check);
    obs.observe(document.body, { childList: true, subtree: true });
    check();
    return () => {
      obs.disconnect();
      if (timer) clearTimeout(timer);
    };
  }, []);

  const close = useCallback(() => {
    markTourSeen(storage(), TOUR_ID);
    setStep(null);
  }, []);

  // Steps visited, for Back: the path depends on what the cluster has.
  const [visited, setVisited] = useState<number[]>([]);

  // `clicked`: the user clicked the step's target themselves, which already
  // did what onNext would.
  const next = useCallback(
    async (clicked = false) => {
      if (step === null) return;
      const opens = !!STEPS[step].onNext;
      if (!clicked) STEPS[step].onNext?.();
      // After opening a view, give it a moment to show what it has (setup
      // card, progress or results) before choosing where to go.
      let k = nextApplicable(step);
      for (let waited = 0; opens && waited < 2500; waited += 150) {
        await new Promise((r) => setTimeout(r, 150));
        k = nextApplicable(step);
        if (k >= 0 && STEPS[k].when) break;
      }
      if (k < 0) {
        close();
        return;
      }
      setVisited((v) => [...v, step]);
      setStep(k);
    },
    [step, close],
  );

  const back = useCallback(() => {
    setVisited((v) => {
      if (v.length <= 1) return v;
      setStep(v[v.length - 1]);
      return v.slice(0, -1);
    });
  }, []);

  // Follow the target while it moves: views open, panes resize, lists load.
  useLayoutEffect(() => {
    if (step === null) return;
    let raf = 0;
    let last = '';
    const tick = () => {
      const r = visibleRect(STEPS[step].target);
      const card = cardRef.current?.getBoundingClientRect();
      const key = JSON.stringify([
        r,
        card?.width,
        card?.height,
        window.innerWidth,
        window.innerHeight,
      ]);
      if (key !== last) {
        last = key;
        setRect(r);
        setPlace(
          placeCard(
            r && pad(r),
            { width: card?.width ?? 340, height: card?.height ?? 180 },
            {
              width: window.innerWidth,
              height: window.innerHeight,
            },
          ),
        );
      }
      raf = requestAnimationFrame(tick);
    };
    tick();
    return () => cancelAnimationFrame(raf);
  }, [step]);

  // A click on the spotlighted element is the user doing what the step
  // points at (connecting metrics, opening a workload): the tour steps aside,
  // and the click goes through.
  useEffect(() => {
    if (step === null) return;
    const onClick = (e: MouseEvent) => {
      const t = e.target as Node | null;
      for (const sel of STEPS[step].target ?? []) {
        const el = document.querySelector(sel);
        if (el && t && el.contains(t)) {
          if (STEPS[step].onNext) next(true);
          else close();
          return;
        }
      }
    };
    document.addEventListener('click', onClick, true);
    return () => document.removeEventListener('click', onClick, true);
  }, [step, close, next]);

  useEffect(() => {
    if (step === null) return;
    // Focus the main action, so Enter and Space work and screen readers
    // land inside the dialog.
    primaryRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        close();
      } else if (
        e.key === 'ArrowRight' ||
        (e.key === 'Enter' &&
          (e.target as HTMLElement | null)?.tagName !== 'BUTTON')
      ) {
        e.preventDefault();
        e.stopPropagation();
        next();
      } else if (e.key === 'ArrowLeft') {
        e.preventDefault();
        e.stopPropagation();
        back();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [step, next, back, close]);

  if (step === null) return null;
  const s = STEPS[step];
  const first = step === 0;
  const last = nextApplicable(step) < 0 && !s.onNext;
  const spot = rect && pad(rect);

  return createPortal(
    <div className="ft-root" role="presentation">
      {spot ? (
        <>
          <div
            className="ft-spot"
            style={{
              top: spot.top,
              left: spot.left,
              width: spot.width,
              height: spot.height,
            }}
          />
          {/* Clicks outside the spotlight are held back; inside it, the real
              UI works, and using it ends the tour. */}
          <div
            className="ft-block"
            style={{ top: 0, left: 0, right: 0, height: Math.max(0, spot.top) }}
          />
          <div
            className="ft-block"
            style={{
              top: spot.top + spot.height,
              left: 0,
              right: 0,
              bottom: 0,
            }}
          />
          <div
            className="ft-block"
            style={{
              top: spot.top,
              left: 0,
              width: Math.max(0, spot.left),
              height: spot.height,
            }}
          />
          <div
            className="ft-block"
            style={{
              top: spot.top,
              left: spot.left + spot.width,
              right: 0,
              height: spot.height,
            }}
          />
        </>
      ) : (
        <div className="ft-dim" />
      )}
      <div
        ref={cardRef}
        className={`ft-card ft-${place?.side ?? 'center'}${first ? ' ft-welcome' : ''}`}
        style={{ top: place?.top ?? -9999, left: place?.left ?? -9999 }}
        role="dialog"
        aria-modal="true"
        aria-labelledby="ft-title"
        tabIndex={-1}
      >
        <button
          className="ft-close"
          onClick={close}
          aria-label="Close the tour"
        >
          <Cross2Icon />
        </button>
        {!first && <div className="ft-step">Step {visited.length}</div>}
        <h3 id="ft-title">{s.title}</h3>
        <div className="ft-body">{s.body}</div>
        <div className="ft-actions">
          {first ? (
            <>
              <button className="ft-link" onClick={close}>
                Not now
              </button>
              <button
                ref={primaryRef}
                className="ft-primary"
                onClick={() => next()}
              >
                Show me around
              </button>
            </>
          ) : (
            <>
              <button
                className="ft-link"
                onClick={visited.length > 1 ? back : close}
              >
                {visited.length > 1 ? 'Back' : 'Skip'}
              </button>
              <button
                ref={primaryRef}
                className="ft-primary"
                onClick={() => next()}
              >
                {last ? 'Done' : 'Next'}
              </button>
            </>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
};

const pad = (r: Rect): Rect => ({
  top: r.top - 6,
  left: r.left - 6,
  width: r.width + 12,
  height: r.height + 12,
});

export default FeatureTour;
