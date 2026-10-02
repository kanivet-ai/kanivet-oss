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
  /** A state of the dashboard (no metrics, error, computing, loading): if
   * the dashboard moves on while it shows, the tour follows. */
  state?: boolean;
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
const ERROR = `${IN_DASHBOARD} [data-tour="rs-error"]`;
/** Any of these on screen means the dashboard has decided what to show. */
const SETTLED = [SETUP, ERROR, PROGRESS, RESULTS].join(', ');

/**
 * Things the tour must not cover: dialogs and sheets, the cluster picker and
 * palettes, and a cluster that can't be reached or needs a login (its error
 * pane covers the content, though the sidebar can still show Rightsizing).
 */
const BUSY = [
  '[aria-modal="true"]',
  '.ap-overlay',
  '.modal-backdrop',
  '.command-palette-overlay',
  '.cluster-selector-modal',
  '.quick-cluster-search',
  '.disconnected-overlay',
  '.ks-overlay',
  '.kse-overlay',
  '.component-library-overlay',
  '.cluster-error-pane',
];

/** Something else needs the user's attention: wait, or pause the tour. */
const busy = () => {
  for (const sel of BUSY) {
    for (const el of document.querySelectorAll(sel)) {
      if ((el as HTMLElement).closest('.ft-root')) continue;
      const r = el.getBoundingClientRect();
      if (r.width > 0 && r.height > 0) return true;
    }
  }
  return false;
};

/** How long the dashboard gets to show what it has after the tour opens it:
 * the first request also detects the metrics store, which can take a while
 * on a cold start. */
const SETTLE_TIMEOUT_MS = 20_000;

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
    state: true,
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
    // Reading history failed: the cluster can't be reached, credentials
    // expired, or the metrics store errored.
    target: [ERROR],
    when: ERROR,
    state: true,
    unless: RESULTS,
    title: "Rightsizing can't read this cluster yet",
    body: (
      <p>
        The message says what went wrong, often expired credentials or a metrics
        store that didn't answer. Once it's fixed, Retry brings the
        recommendations in.
      </p>
    ),
  },
  {
    target: [PROGRESS],
    when: PROGRESS,
    state: true,
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
    // The dashboard didn't settle in time: say so instead of vanishing.
    target: [IN_DASHBOARD],
    when: IN_DASHBOARD,
    state: true,
    unless: SETTLED,
    title: 'Rightsizing is still loading',
    body: (
      <p>
        It's checking which metrics this cluster has. It will show what you
        could save, what's at risk and the evidence for every workload, or what
        to connect if it finds no usage history.
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

/** The step that opens the dashboard; state steps follow it. */
const OPEN_STEP = 1;

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
  // Opening the view: waiting for the dashboard to show what it has.
  const [opening, setOpening] = useState(false);
  // Something else took over the screen (a dialog, a cluster error): the
  // tour hides and comes back to the same step when it's gone.
  const [paused, setPaused] = useState(false);
  // Whether a step follows the current one, re-checked as the page changes:
  // a report that finishes computing turns "Done" into "Next".
  const [hasNext, setHasNext] = useState(false);
  // Steps visited, for Back: the path depends on what the cluster has.
  const [visited, setVisited] = useState<number[]>([]);
  const cardRef = useRef<HTMLDivElement>(null);
  const primaryRef = useRef<HTMLButtonElement>(null);
  const run = useRef(0); // bumped on close, so a pending wait stops
  // Set while the tour clicks something itself (opening the view), so the
  // click isn't mistaken for the user clicking the spotlighted element.
  const selfClick = useRef(false);
  // Set from the moment Next is pressed until the step changes: a second
  // press, or a click arriving in between, must not advance twice.
  const advancing = useRef(false);

  // Start once the app is ready for it: a cluster connected (its sidebar
  // shows Rightsizing and no error or login pane covers it), no dialog
  // open, the window visible, and all of that for a moment, so the tour
  // doesn't land on top of a first load or a sign-in.
  useEffect(() => {
    if (shownThisSession || seenTours(storage()).includes(TOUR_ID)) return;
    let readySince = 0;
    const timer = setInterval(() => {
      const ready =
        !document.hidden && visibleRect([START_SELECTOR]) !== null && !busy();
      if (!ready) {
        readySince = 0;
        return;
      }
      readySince ||= Date.now();
      if (Date.now() - readySince < 1500 || shownThisSession) return;
      shownThisSession = true;
      clearInterval(timer);
      setStep(0);
    }, 500);
    return () => clearInterval(timer);
  }, []);

  const close = useCallback(() => {
    run.current++;
    advancing.current = false;
    markTourSeen(storage(), TOUR_ID);
    setStep(null);
    setOpening(false);
  }, []);

  // `clicked`: the user clicked the step's target themselves, which already
  // did what onNext would.
  const next = useCallback(
    async (clicked = false) => {
      if (step === null || opening || advancing.current) return;
      advancing.current = true;
      const token = ++run.current;
      const opens = !!STEPS[step].onNext;
      if (!clicked) {
        selfClick.current = true;
        try {
          STEPS[step].onNext?.();
        } finally {
          selfClick.current = false;
        }
      }
      if (opens) {
        // Give the view time to decide: no metrics, an error, computing, or
        // results. A cold start detects the metrics store first.
        setOpening(true);
        const start = Date.now();
        while (
          Date.now() - start < SETTLE_TIMEOUT_MS &&
          visibleRect([SETTLED]) === null
        ) {
          await new Promise((r) => setTimeout(r, 200));
          if (run.current !== token) {
            advancing.current = false;
            return;
          }
        }
        // Let what just appeared lay out before measuring it.
        await new Promise((r) => setTimeout(r, 250));
        if (run.current !== token) {
          advancing.current = false;
          return;
        }
        setOpening(false);
      }
      advancing.current = false;
      const k = nextApplicable(step);
      if (k < 0) {
        close();
        return;
      }
      setVisited((v) => [...v, step]);
      setStep(k);
    },
    [step, opening, close],
  );

  const back = useCallback(() => {
    if (opening) return;
    setVisited((v) => {
      if (v.length <= 1) return v;
      setStep(v[v.length - 1]);
      return v.slice(0, -1);
    });
  }, [opening]);

  // Watch for anything that should pause the tour, and for steps that
  // become available (a report that finishes computing). A timer, not
  // animation frames: those stop in a window that isn't being drawn.
  useEffect(() => {
    if (step === null) return;
    let freeSince = 0;
    const check = () => {
      const now = Date.now();
      if (busy()) {
        freeSince = 0;
        setPaused(true);
      } else {
        freeSince ||= now;
        if (now - freeSince >= 600) setPaused(false);
      }
      // The dashboard moved on (computing → results or error, loading →
      // anything): go to the step for what it shows now.
      if (STEPS[step].state && !applies(STEPS[step])) {
        const k = nextApplicable(OPEN_STEP);
        if (k >= 0 && k !== step) {
          setStep(k);
          return;
        }
      }
      setHasNext(nextApplicable(step) >= 0);
    };
    check();
    const timer = setInterval(check, 300);
    return () => clearInterval(timer);
  }, [step]);

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
    if (step === null || paused) return;
    const onClick = (e: MouseEvent) => {
      if (selfClick.current) return;
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
  }, [step, paused, close, next]);

  useEffect(() => {
    if (step === null || paused) return;
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
  }, [step, paused, next, back, close]);

  if (step === null || paused) return null;
  const s = STEPS[step];
  const first = step === 0;
  const last = !hasNext && !s.onNext;
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
        <h3 id="ft-title">{opening ? 'Opening Rightsizing…' : s.title}</h3>
        <div className="ft-body" aria-live="polite">
          {opening ? (
            <p className="ft-opening">
              <span className="ft-spinner" aria-hidden="true" />
              Checking which metrics this cluster has.
            </p>
          ) : (
            s.body
          )}
        </div>
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
                disabled={opening}
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
